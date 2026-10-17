# echonet-lite

ECHONET Lite 対応エアコンの情報取得・操作を行う Go ライブラリと CLI の試作です。
Linux / Go 1.25 以降で動作し、外部依存はありません。

## インストール

```sh
go install github.com/otknoy/echonet-lite/cmd/echonet@latest
echonet --help
```

インストール先は `GOBIN`、未設定なら `$(go env GOPATH)/bin` です。そのディレクトリを `PATH` に追加してください。

## CLI

```sh
go build -buildvcs=false -o /tmp/echonet ./cmd/echonet
/tmp/echonet aircon discover --interface eth0
/tmp/echonet aircon status --interface eth0 --host 192.168.1.10 --eoj 013001 --format json
/tmp/echonet aircon power on --interface eth0 --host 192.168.1.10 --eoj 013001
/tmp/echonet aircon mode cool --interface eth0 --host 192.168.1.10 --eoj 013001
/tmp/echonet aircon temperature 26 --interface eth0 --host 192.168.1.10 --eoj 013001
```

`--host`・`--eoj` は探索結果に合わせて指定してください。NIC 候補が一つなら `--interface` を省略できます。
共通オプションは `--interface`、`--timeout 3s`、`--format text|json` です。
モードは `auto|cool|heat|dry|fan`、設定温度は整数の 0〜50℃です。実機固有の制限は機器の応答で確認します。

取得項目は電源、運転モード、設定温度、室温です。JSON の各項目は `value` と `state`（`ok`・`unsupported`・`error`）を持ちます。
未対応と取得失敗は区別し、値がない場合は `null` にします。
終了コードは成功 `0`、通信失敗・不可応答 `1`、引数不正 `2` です。部分失敗でも取得できた値は出力します。
設定成功は `accepted`（受理）と表示し、設定応答がない場合は結果不明として終了します。自動再送はしません。

## ライブラリ

低レイヤーは規格の EOJ・EPC・Get・SetC を使います。

```go
client, err := echonet.Open(echonet.Options{Interface: "eth0"})
if err != nil { return err }
defer client.Close()

object := client.Node(netip.MustParseAddr("192.168.1.10")).
    Object(echonet.EOJ{0x01, 0x30, 0x01})
response, err := object.Get(ctx, aircon.OperationStatus)
// 不可応答でも response に部分結果が入ります。
```

高レイヤーは同じオブジェクトを包みます。

```go
ac, err := aircon.New(object)
if err != nil { return err }
status, err := ac.Status(ctx)
err = ac.SetPower(ctx, true)
err = ac.SetMode(ctx, aircon.Cool)
err = ac.SetTemperature(ctx, 26)
```

インポートは `github.com/otknoy/echonet-lite/echonet` と `github.com/otknoy/echonet-lite/aircon` です。
`Status` は項目ごとの結果とエラーを返します。高レイヤーは操作の都度プロパティマップを取得し、対応可否を確認します。
`errors.As` で `*echonet.ServiceError`・`*echonet.SettingUnknownError` を、`errors.Is` で context の期限・キャンセルを判別できます。

## 検証

```sh
go test -race ./...
go vet ./...
```

通常のテストはメモリ上の仮想機器とローカルループバックのみを使用します。
[設計](docs/README.md)・[実機確認手順と制限](docs/verification.md)も参照してください。
