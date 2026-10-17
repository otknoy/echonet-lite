# echonet-lite

ECHONET Lite 対応エアコンの情報取得・操作を行う Go ライブラリと CLI の試作です。
Linux / Go 1.25 以降で動作します。

## CLI のインストール

```sh
go install github.com/otknoy/echonet-lite/cmd/echonet@latest
echonet --help
```

特定のリリースを指定する場合は、`@latest` を `@v0.1.0` などの公開済みタグに置き換えてください。

## CLI の使い方

エアコンの ECHONET Lite 機能を有効にし、同じ LAN に接続して実行します。
`eth0` は利用するネットワークインターフェース名に置き換えてください。

```sh
# エアコンを探索（IP アドレスと EOJ を表示）
echonet aircon discover --interface eth0

# 状態を取得（電源・運転モード・設定温度・室温）
echonet aircon status --interface eth0 --host 192.168.1.10 --eoj 013001

# 電源を ON / OFF
echonet aircon power on --interface eth0 --host 192.168.1.10 --eoj 013001
echonet aircon power off --interface eth0 --host 192.168.1.10 --eoj 013001

# 冷房に変更し、設定温度を26℃に変更
echonet aircon mode cool --interface eth0 --host 192.168.1.10 --eoj 013001
echonet aircon temperature 26 --interface eth0 --host 192.168.1.10 --eoj 013001
```

JSON で取得する場合は `--format json` を指定します。

```sh
echonet aircon status --interface eth0 --host 192.168.1.10 --eoj 013001 --format json
```

`--host`・`--eoj` は探索結果に合わせて指定してください。NIC 候補が一つなら `--interface` を省略できます。
共通オプションは `--interface`、`--timeout 3s`、`--format text|json` です。
モードは `auto|cool|heat|dry|fan`、設定温度は整数の 0〜50℃です。実機固有の制限は機器の応答で確認します。

取得項目は電源、運転モード、設定温度、室温です。JSON の各項目は `value` と `state`（`ok`・`unsupported`・`error`）を持ちます。
未対応と取得失敗は区別し、値がない場合は `null` にします。
終了コードは成功 `0`、通信失敗・不可応答 `1`、引数不正 `2` です。部分失敗でも取得できた値は出力します。
設定成功は `accepted`（受理）と表示し、設定応答がない場合は結果不明として終了します。自動再送はしません。

## 設定ファイル

次のコマンドで対話式に設定し、`~/.config/echonet/config.yaml` に保存します。既存ファイルを作り直す場合は `echonet config init --force` を使います。

```sh
echonet config init
```

1. ネットワークインターフェース（eth0 など）を選択します。
2. 探索したエアコンごとに、登録するかを選び、任意の名前を付けます。
3. 登録した対象から `current_target` を選択します。

番号または名前で選択できます。登録しない場合は interface のみ保存します。途中で中断した場合は保存せず、既存設定も保持します。
探索時間は `--timeout 5s`、空のひな形だけ作る場合は `--empty` を指定します。
`XDG_CONFIG_HOME` が設定されている場合は、その配下の `echonet/config.yaml` を使います。
手動で編集する場合は、[設定サンプル](config.example.yaml)も参照してください。

```yaml
interface: eth0
current_target: living

targets:
  living:
    host: 192.168.1.10
    eoj: "013001"
  bedroom:
    host: 192.168.1.11
    eoj: "013001"
```

```sh
echonet config list          # 登録済み対象を表示（現在の対象に *）
echonet config use bedroom   # 切り替え先を保存
echonet aircon status
echonet aircon power on
echonet aircon temperature 26
```

`--interface` は設定より優先します。`--host`・`--eoj` は両方指定すると今回の操作だけ対象を上書きできます。
別のファイルを使う場合は、`echonet config use bedroom --config ./config.yaml` のように `--config PATH` を指定してください。
設定ファイルがなければ、従来どおり引数で指定できます。`discover` は設定の interface のみ利用します。

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
