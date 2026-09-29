package awsx

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

const portForwardDocument = "AWS-StartPortForwardingSessionToRemoteHost"

// PortForwardRequest is what a target resolves to right before StartSession.
type PortForwardRequest struct {
	Profile    string
	Region     string
	InstanceID string
	Host       string
	RemotePort int
	LocalPort  int
}

// Session carries everything session-manager-plugin needs on its argv.
type Session struct {
	SessionID  string
	Region     string
	Profile    string
	Endpoint   string
	SessionArg string // JSON of StartSession output
	ParamsArg  string // JSON of StartSession input
}

// SessionResponseEnv is read by session-manager-plugin when argv[1] equals
// this name, keeping the session token out of `ps` output. The AWS CLI does
// the same (plugin 1.2.463.0 or newer).
const SessionResponseEnv = "AWS_SSM_START_SESSION_RESPONSE"

// PluginArgs returns argv (without argv[0]) exactly as the AWS CLI builds it.
func (s *Session) PluginArgs() []string {
	return []string{SessionResponseEnv, s.Region, "StartSession", s.Profile, s.ParamsArg, s.Endpoint}
}

// StartSession opens a port-forwarding session on the bastion.
func StartSession(ctx context.Context, cfg aws.Config, req PortForwardRequest) (*Session, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	params := map[string][]string{
		"host":            {req.Host},
		"portNumber":      {strconv.Itoa(req.RemotePort)},
		"localPortNumber": {strconv.Itoa(req.LocalPort)},
	}
	input := &ssm.StartSessionInput{
		Target:       aws.String(req.InstanceID),
		DocumentName: aws.String(portForwardDocument),
		Parameters:   params,
		Reason:       aws.String("ssmtun port forward to " + req.Host),
	}
	out, err := ssm.NewFromConfig(cfg).StartSession(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("ssm:StartSession on %s: %w", req.InstanceID, err)
	}

	sessionJSON, err := json.Marshal(map[string]string{
		"SessionId":  aws.ToString(out.SessionId),
		"TokenValue": aws.ToString(out.TokenValue),
		"StreamUrl":  aws.ToString(out.StreamUrl),
	})
	if err != nil {
		return nil, err
	}
	paramsJSON, err := json.Marshal(map[string]any{
		"Target":       req.InstanceID,
		"DocumentName": portForwardDocument,
		"Parameters":   params,
	})
	if err != nil {
		return nil, err
	}
	region := cfg.Region
	if region == "" {
		region = req.Region
	}
	return &Session{
		SessionID:  aws.ToString(out.SessionId),
		Region:     region,
		Profile:    req.Profile,
		Endpoint:   fmt.Sprintf("https://ssm.%s.amazonaws.com", region),
		SessionArg: string(sessionJSON),
		ParamsArg:  string(paramsJSON),
	}, nil
}

// TerminateSession is best-effort cleanup after the plugin exits.
func TerminateSession(ctx context.Context, cfg aws.Config, sessionID string) error {
	if sessionID == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := ssm.NewFromConfig(cfg).TerminateSession(ctx, &ssm.TerminateSessionInput{SessionId: aws.String(sessionID)})
	return err
}

// ResolveInstanceByTag finds exactly one running instance with tag Key=Value.
func ResolveInstanceByTag(ctx context.Context, cfg aws.Config, tag string) (string, error) {
	key, value, ok := strings.Cut(tag, "=")
	if !ok {
		return "", fmt.Errorf("instance_tag %q must look like Key=Value", tag)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := ec2.NewFromConfig(cfg).DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("tag:" + key), Values: []string{value}},
			{Name: aws.String("instance-state-name"), Values: []string{"running"}},
		},
	})
	if err != nil {
		return "", fmt.Errorf("ec2:DescribeInstances for %s: %w", tag, err)
	}
	var ids []string
	for _, r := range out.Reservations {
		for _, i := range r.Instances {
			ids = append(ids, aws.ToString(i.InstanceId))
		}
	}
	switch len(ids) {
	case 0:
		return "", fmt.Errorf("no running instance matches %s", tag)
	case 1:
		return ids[0], nil
	default:
		return "", fmt.Errorf("%d running instances match %s (%s); use `instance` instead", len(ids), tag, strings.Join(ids, ", "))
	}
}
