// Package awsx wraps the AWS SDK for the handful of calls ssmtun needs:
// SSO device-code login, credential checks and SSM session lifecycle.
package awsx

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssooidc"
	"github.com/aws/aws-sdk-go-v2/service/ssooidc/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

const clientName = "ssmtun"

// SSOTarget describes where an SSO login must be performed.
type SSOTarget struct {
	SessionName string // sso-session name, empty for legacy profiles
	StartURL    string
	Region      string
}

// tokenCache mirrors the JSON the AWS CLI writes to ~/.aws/sso/cache so
// both tools can share one login.
type tokenCache struct {
	StartURL              string `json:"startUrl"`
	Region                string `json:"region"`
	AccessToken           string `json:"accessToken"`
	ExpiresAt             string `json:"expiresAt"`
	ClientID              string `json:"clientId,omitempty"`
	ClientSecret          string `json:"clientSecret,omitempty"`
	RegistrationExpiresAt string `json:"registrationExpiresAt,omitempty"`
	RefreshToken          string `json:"refreshToken,omitempty"`
}

// LoadProfile loads the shared config for a profile without resolving credentials.
func LoadProfile(ctx context.Context, profile string) (aws.Config, error) {
	return config.LoadDefaultConfig(ctx, config.WithSharedConfigProfile(profile))
}

// ResolveSSO figures out which SSO session/start URL a profile authenticates with.
func ResolveSSO(ctx context.Context, profile string) (*SSOTarget, error) {
	sc, err := config.LoadSharedConfigProfile(ctx, profile)
	if err != nil {
		return nil, fmt.Errorf("profile %q: %w", profile, err)
	}
	if sc.SSOSession != nil {
		return &SSOTarget{
			SessionName: sc.SSOSession.Name,
			StartURL:    sc.SSOSession.SSOStartURL,
			Region:      sc.SSOSession.SSORegion,
		}, nil
	}
	if sc.SSOStartURL != "" {
		return &SSOTarget{StartURL: sc.SSOStartURL, Region: sc.SSORegion}, nil
	}
	return nil, nil
}

// CheckCredentials returns nil when the profile can currently obtain credentials.
// It calls sts:GetCallerIdentity, the same check the AWS CLI users rely on.
func CheckCredentials(ctx context.Context, profile string) error {
	cfg, err := LoadProfile(ctx, profile)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, err = sts.NewFromConfig(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	return err
}

// EnsureLogin checks credentials and runs the SSO device flow when needed.
// Non-SSO profiles are only checked; failures are reported as-is.
func EnsureLogin(ctx context.Context, profile string, out io.Writer) error {
	if err := CheckCredentials(ctx, profile); err == nil {
		return nil
	}
	target, err := ResolveSSO(ctx, profile)
	if err != nil {
		return err
	}
	if target == nil {
		return fmt.Errorf("profile %q has no valid credentials and is not an SSO profile", profile)
	}
	if err := DeviceLogin(ctx, target, out); err != nil {
		return err
	}
	return CheckCredentials(ctx, profile)
}

// DeviceLogin runs the OIDC device authorization flow and writes the token
// cache in AWS CLI format.
func DeviceLogin(ctx context.Context, target *SSOTarget, out io.Writer) error {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(target.Region),
		config.WithCredentialsProvider(aws.AnonymousCredentials{}))
	if err != nil {
		return err
	}
	client := ssooidc.NewFromConfig(cfg)

	reg, err := client.RegisterClient(ctx, &ssooidc.RegisterClientInput{
		ClientName: aws.String(clientName),
		ClientType: aws.String("public"),
		Scopes:     []string{"sso:account:access"},
	})
	if err != nil {
		return fmt.Errorf("register OIDC client: %w", err)
	}

	auth, err := client.StartDeviceAuthorization(ctx, &ssooidc.StartDeviceAuthorizationInput{
		ClientId:     reg.ClientId,
		ClientSecret: reg.ClientSecret,
		StartUrl:     aws.String(target.StartURL),
	})
	if err != nil {
		return fmt.Errorf("start device authorization: %w", err)
	}

	fmt.Fprintf(out, "Attempting to open the SSO authorization page in your browser.\n")
	fmt.Fprintf(out, "If it does not open, visit:\n\n  %s\n\nand enter the code: %s\n\n", aws.ToString(auth.VerificationUri), aws.ToString(auth.UserCode))
	openBrowser(aws.ToString(auth.VerificationUriComplete))

	interval := time.Duration(auth.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(time.Duration(auth.ExpiresIn) * time.Second)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
		if time.Now().After(deadline) {
			return errors.New("device authorization expired before it was approved")
		}
		tok, err := client.CreateToken(ctx, &ssooidc.CreateTokenInput{
			ClientId:     reg.ClientId,
			ClientSecret: reg.ClientSecret,
			GrantType:    aws.String("urn:ietf:params:oauth:grant-type:device_code"),
			DeviceCode:   auth.DeviceCode,
		})
		if err != nil {
			var pending *types.AuthorizationPendingException
			var slow *types.SlowDownException
			if errors.As(err, &pending) {
				continue
			}
			if errors.As(err, &slow) {
				interval += 5 * time.Second
				continue
			}
			return fmt.Errorf("create token: %w", err)
		}
		cache := tokenCache{
			StartURL:              target.StartURL,
			Region:                target.Region,
			AccessToken:           aws.ToString(tok.AccessToken),
			ExpiresAt:             time.Now().UTC().Add(time.Duration(tok.ExpiresIn) * time.Second).Format(time.RFC3339),
			ClientID:              aws.ToString(reg.ClientId),
			ClientSecret:          aws.ToString(reg.ClientSecret),
			RegistrationExpiresAt: time.Unix(reg.ClientSecretExpiresAt, 0).UTC().Format(time.RFC3339),
			RefreshToken:          aws.ToString(tok.RefreshToken),
		}
		if err := writeTokenCache(target, cache); err != nil {
			return err
		}
		fmt.Fprintf(out, "Successfully logged into Start URL: %s\n", target.StartURL)
		return nil
	}
}

// cachePath follows the AWS CLI: sha1 of the sso-session name, or of the
// start URL for legacy profiles.
func cachePath(target *SSOTarget) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	key := target.SessionName
	if key == "" {
		key = target.StartURL
	}
	sum := sha1.Sum([]byte(key))
	return filepath.Join(home, ".aws", "sso", "cache", hex.EncodeToString(sum[:])+".json"), nil
}

func writeTokenCache(target *SSOTarget, cache tokenCache) error {
	path, err := cachePath(target)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(cache)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func openBrowser(url string) {
	if url == "" || os.Getenv("SSMTUN_NO_BROWSER") != "" {
		return
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
