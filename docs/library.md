# ライブラリの使い方

`echonet` は規格に沿った共通 API、`aircon` はエアコン用の型付き API を提供する。
ユーザーの CLI 設定は自動で読み込まない。

## 接続と取得

```go
import (
    "context"
    "net/netip"

    "github.com/otknoy/echonet-lite/aircon"
    "github.com/otknoy/echonet-lite/echonet"
)

func readStatus(ctx context.Context) (aircon.Status, error) {
    client, err := echonet.Open(echonet.Options{Interface: "eth0"})
    if err != nil {
        return aircon.Status{}, err
    }
    defer func() { _ = client.Close() }()

    object := client.Node(netip.MustParseAddr("192.168.1.10")).
        Object(echonet.EOJ{0x01, 0x30, 0x01})
    ac, err := aircon.New(object)
    if err != nil {
        return aircon.Status{}, err
    }
    return ac.Status(ctx)
}
```

IP・EOJ は実機に合わせて指定する。`Client.Discover(ctx)` でノードとオブジェクトの一覧を取得できる。
Node・Object・Aircon の生成だけでは通信せず、取得・設定時に通信する。

## API

| 層 | 操作 |
|---|---|
| `echonet` | `Object.Get(ctx, epcs...)`、`Object.SetC(ctx, properties...)` |
| `echonet` | `Object.PropertyMaps(ctx)` で取得・設定・通知に対応する EPC を取得 |
| `aircon` | `Aircon.Status(ctx)` で電源・モード・設定温度・室温を取得 |
| `aircon` | `SetPower(ctx, true)`、`SetMode(ctx, aircon.Cool)`、`SetTemperature(ctx, 26)` |

低レイヤーは EPC と生 EDT を直接指定できる。

```go
response, err := object.Get(ctx, aircon.OperationStatus)
response, err = object.SetC(ctx, echonet.Property{
    EPC: aircon.TemperatureSetting,
    EDT: []byte{26},
})
```

高レイヤーは操作ごとにプロパティマップで対応可否を確認し、値を変換・検証する。
設定温度は整数の0〜50℃を受け付け、機器・モード固有の制限は機器の応答で確認する。

## 結果とエラー

- `Response` は発信元・TID・SEOJ・DEOJ・ESV・EPC/EDT を保持する。不可応答でも部分結果を返す。
- `Status` の各項目は `Value`・`State`・`Error` を持ち、未対応と取得失敗を区別する。部分失敗時も取得済みの値を返す。
- `errors.As` で `*echonet.ServiceError`（不可応答）、`*echonet.SettingUnknownError`（設定結果不明）を判別できる。
- `errors.Is` で `echonet.ErrUnsupported`、context の期限切れ・キャンセルを判別できる。
- Client の終了は `Close()`。設定要求は自動再送しない。
