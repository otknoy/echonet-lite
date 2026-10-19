# CI

## CI

PR・main への push で、以下を確認する。Go のバージョンは `go.mod` に従う。

- gofmt・モジュールファイルの整合性
- `go test -race ./...`
- CLI のビルド
- golangci-lint（v2.15.0）

実機の探索・操作は行わない。
