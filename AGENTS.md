# Coding agent 向けガイド

日本語で簡潔かつ丁寧に回答する。

## 文書の索引

| 文書 | 内容 |
|---|---|
| [README](README.md) | 利用者向けのインストール・初期設定・基本操作 |
| [CLI と設定](docs/cli.md) | 引数、YAML、対象切り替え、出力・終了コード |
| [ライブラリ](docs/library.md) | 低レイヤー・高レイヤー API の使い方 |
| [設計](docs/architecture.md) | パッケージの責務、通信とエラー処理の方針 |
| [検証](docs/verification.md) | 開発コマンド、実機試験、対応範囲・制限 |
| [CI/CD](docs/ci.md) | GitHub Actions のチェック、タグによるリリース |

## 開発方針

- モジュールは `github.com/otknoy/echonet-lite`、Go 1.25 以降、通信実装は Linux 向け。
- `echonet` は規格の EOJ・EPC・EDT・Get・SetC に沿った共通 API、`aircon` は型付き操作を提供する。通信処理を重複させない。
- CLI と設定処理は `internal/cli`・`internal/config` に置き、公開ライブラリからユーザー設定を読み込まない。
- 未対応・取得失敗・設定結果不明を区別する。設定要求を自動再送しない。
- `config init` は完了時のみ保存する。上書きには `--force` が必要。中断・失敗時は元の設定を保持する。
- README は利用者目線で短く保つ。詳細は docs の該当文書に集約し、構成変更時はこの索引も更新する。

## 検証・リリース

コード変更後は `gofmt`、`go test -race ./...`、`golangci-lint run -v`、`go build -o /tmp/echonet ./cmd/echonet` を実行する。
文書のみの変更ではリンクと記載内容を確認する。
自動テストは仮想機器を使い、実機の探索・操作は明示された試験で行う。
スキップした試験や環境制約は報告し、実機確認済みとは扱わない。

コミット・push・タグ公開は依頼された場合に行う。
バージョンは Git タグで管理し、パッチ更新は最新の対象バージョンの末尾のみを一つ上げる（例: `v0.0.2` → `v0.0.3`）。
