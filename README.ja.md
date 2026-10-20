# ssmtun

SSM 管理下の踏み台経由で RDS / Aurora / ElastiCache 等へポートフォワードするトンネルを、名前付きで起動・監視・自動再接続する CLI です。

`aws ssm start-session` を包むシェルスクリプトを各自が書く代わりに、チームで 1 つの設定を共有できます。

- **名前付きターゲット**を YAML で定義。`~/.config/ssmtun/config.d/` かプロジェクト直下の `.ssmtun.yaml` で共有
- **トンネルごとに独立したプロセス**。15 秒ごとに経路全体を疎通確認し、切れたらバックオフ付きで自動再接続
- **AWS SSO ログイン内蔵**（デバイスコードフロー。トークンキャッシュは AWS CLI と互換）
- **確実な後始末**。Ctrl-C や `ssmtun down` で SSM セッションを終了し、`session-manager-plugin` の孤児プロセスがポートを握り続けることがない
- **AWS CLI 不要**。必要なのは Session Manager plugin だけ

## インストール

```sh
brew install --cask session-manager-plugin   # 初回のみ
brew install ak-inori/tap/ssmtun
```

## 設定

```sh
ssmtun config init   # ~/.config/ssmtun/config.yaml を生成
ssmtun doctor        # plugin・設定・AWS プロファイルを確認
```

```yaml
defaults:
  health_interval: 15s

targets:
  app-dev:
    profile: app                       # ~/.aws/config のプロファイル（SSO 対応）
    instance: i-0123456789abcdef0      # 踏み台。instance_tag: Name=bastion も可
    host: dev-app.xxxx.ap-northeast-1.rds.amazonaws.com
    port: 5432
    local_port: 20001
    color: cyan                        # ログ接頭辞の色。省略時は自動割り当て（後述）
```

設定ファイルは次の順で読み込み、後のファイルが同名ターゲットを上書きします。

1. `~/.config/ssmtun/config.yaml`
2. `~/.config/ssmtun/config.d/*.yaml`（チーム配布用）
3. カレントディレクトリから上に探した `.ssmtun.yaml`（リポジトリにコミットする用）

`color` には基本色の名前（`black` `red` `green` `yellow` `blue` `magenta` `cyan` `white`）、その明るい版（`bright-red` など）、256 色パレットの番号（`0`〜`255`）、カラーコード（`#ff8800` や `#f80`）を指定できます。カラーコードは端末が 24 ビットカラーに対応している必要があり、macOS 標準の Terminal.app では表示されません。色を省略したターゲットには組み込みのパレットから順に割り当てられ、同じ `up` で他のターゲットが使っている色は避けられます。

## 使い方

| コマンド | 内容 |
|---|---|
| `ssmtun up <target>...` | フォアグラウンドで起動。Ctrl-C で全部停止。ログ接頭辞はターゲットごとに色分け（`--no-color` か `NO_COLOR` で無効） |
| `ssmtun up -d <target>...` | バックグラウンドで起動し、接続完了を確認して戻る |
| `ssmtun status` | 稼働中トンネルの一覧、稼働時間、再接続回数 |
| `ssmtun down <target>... \| --all` | 停止。`--force` でポートを握る他プロセスも kill |
| `ssmtun login` | 設定中のプロファイルの SSO 認証を更新 |
| `ssmtun completion fish` | ターゲット名を補完するシェル補完 |

DB クライアントは `127.0.0.1:<local_port>` を向けたままで構いません。再接続してもポートは変わりません。

## 仕組み

Session Manager はポートフォワードセッションを無操作 20 分（既定）で切り、WebSocket が張り替えに失敗して落ちることもあります。各ワーカーは次のように動きます。

1. SDK で `ssm:StartSession` を呼び、`session-manager-plugin` を直接の子プロセスとして起動（AWS CLI と同じ方式）
2. PostgreSQL のターゲットでは、`health_interval` ごとにトンネル経由で `CancelRequest` を送る（サーバー側にログを残さず切断で応答）。経路全体の疎通確認であり、同時にアイドルタイマーもリセットする
   MySQL と `tcp` のターゲットはプローブしない。トンネル経由の接続は必ずリモートまで届き、MySQL は認証前に切れた接続を踏み台の IP ごとに数え、`max_connect_errors` に達すると `Host is blocked because of many connection errors` で拒否するため。これらは plugin プロセスの監視だけで管理し、無操作の場合はアイドルタイムアウトで切れたあと再接続する
3. プローブが 2 回連続で失敗するか plugin が終了したら、セッションを片付けて 1〜30 秒の指数バックオフで再接続
4. 認証情報が切れたら `auth_required` として待機。別ターミナルで `ssmtun login` すれば自動で復帰

状態は `~/.local/state/ssmtun/` に保存され、デタッチしたワーカーのログは同ディレクトリの `<target>.log` に出ます。

## 要件

- `session-manager-plugin` 1.2.463.0 以上
- 踏み台の SSM Agent 3.1.1476.0 以上（それ未満はリモートホスト転送が 30 秒無操作で切れるバグあり）
- IAM: `ssm:StartSession`、`ssm:TerminateSession`、`sts:GetCallerIdentity`、`instance_tag` 利用時は `ec2:DescribeInstances`
