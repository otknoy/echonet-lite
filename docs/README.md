# ECHONET Lite エアコン操作ライブラリ・CLI 設計

実装済みの試作に対応した設計です。使い方は [README](../README.md)、検証と制限は [検証手順](verification.md) を参照してください。

## 方針

初期版は LAN 内の家庭用エアコンの情報取得・操作に絞る。
Go を採用し、CLI は共通のライブラリを利用する。

## 初期版の機能

| 機能 | 内容 |
|---|---|
| 探索 | LAN 内の家庭用エアコンを一覧表示 |
| 情報取得 | 電源状態、運転モード、設定温度、室温 |
| 操作 | 電源 ON/OFF、運転モード変更、設定温度変更 |
| 出力 | 人向け表示と JSON |

運転モードは自動・冷房・暖房・除湿・送風を候補とし、機器が対応するものだけ扱う。
室温など取得できない項目は「未対応」または「取得失敗」とし、0などで補完しない。
他の機器、通知監視、風量・風向、タイマー、IPv6、Wi-SUN/Bルートは後続対応とする。

## 構成

```text
cmd/echonet/   CLI
echonet/      通信開始・探索・要求管理
aircon/       型付き API、定数・値変換・入力検証
internal/     電文処理、UDP 通信、ローカルノード応答
```

公開 API は規格の EOJ・EPC・EDT・サービス名に合わせる。
初期版の機器対応はエアコンに絞り、共通のオブジェクト操作を基盤にする。

## API 方針

公開 API を二層に分け、高レイヤーが低レイヤーを利用する。

| 層 | 用途 | API の例 |
|---|---|---|
| 低レイヤー `echonet` | 規格の用語でオブジェクトを操作 | `Object.Get`、`Object.SetC`、EPC・EDT |
| 高レイヤー `aircon` | 型付きの値でエアコンを操作 | `Aircon.Status`、`Aircon.SetPower`、`Aircon.SetTemperature` |

```text
CLI / 利用アプリ → aircon → echonet → 内部の電文処理・UDP
利用アプリ ─────────────→ echonet
```

両方を公開し、用途に応じて選べるようにする。通信・要求照合・再送方針は低レイヤーに集約する。

### 低レイヤー

ECHONET Lite を知っていれば、同じ用語と操作で使える API にする。
**オブジェクトに対して EPC を指定して Get・SetC を行う**。

| 規格の概念 | API での表現 |
|---|---|
| ノード | `Node`（通信先 IP） |
| EOJ | `EOJ`（クラスグループ・クラス・インスタンスの3バイト） |
| 機器オブジェクト | `Node.Object(eoj)` |
| EPC | `EPC`（1バイト）、機器別の定数 |
| EDT | `[]byte`、必要に応じ機器別の変換関数 |
| Get / SetC | `Object.Get` / `Object.SetC` |
| 応答 | ESV と EPC・EDT を保持する `Response` |

API 使用例（エラー処理は省略）:

```go
client, err := echonet.Open(options)
defer client.Close()

node := client.Node(netip.MustParseAddr("192.168.1.10"))
ac := node.Object(echonet.EOJ{0x01, 0x30, 0x01})

// EPC を指定して取得。定数は EPC 型で、生コードも指定できる。
res, err := ac.Get(ctx,
    aircon.OperationStatus,
    aircon.OperationMode,
    aircon.TemperatureSetting,
)

// EPC と EDT を指定して設定。
res, err = ac.SetC(ctx, echonet.Property{
    EPC: aircon.TemperatureSetting,
    EDT: []byte{26},
})
```

- `Get(ctx, epcs ...EPC)` と `SetC(ctx, properties ...Property)` は複数プロパティを一電文で扱う。順序を保持し、暗黙に分割しない。
- `Response` は応答 ESV とプロパティ一覧を保持する。不可応答は応答本体と型付きエラーを両方返し、取得できた値も残す。
- 相手 IP・TID・SEOJ・DEOJ も応答から参照可能にする。要求の TID・SEOJ は Client が管理し、DEOJ は Object の EOJ から決める。
- `Client.Discover(ctx)` はノードとオブジェクト一覧を返す。CLI は家庭用エアコンクラスに絞って表示する。IP・EOJ が既知なら探索を省略できる。
- `Object.PropertyMaps(ctx)` で取得・設定・通知の対応 EPC を取得できる。未取得は「非対応」と区別する。
- `Node`・`Object` の生成だけでは通信しない。通信するメソッドは `ctx` を受け取り、エラーを返す。
- `aircon` パッケージは EPC 定数・値変換・検証を提供する。生 EDT の指定も可能にし、通信失敗と変換失敗を区別する。
- `Client.Close()` がソケットと待機要求を終了する。共通機能は Go の構造体の合成で共有する。

初期版の公開サービスは Get・SetC に絞る。SetI・INF_REQ・SetGet は追加時も規格名をそのまま使う。

### 高レイヤー

同じ Object をエアコン用の型で包み、プロパティの変換・検証を提供する。

```go
ac, err := aircon.New(object) // EOJ のクラスを検証。通信はしない。
status, err := ac.Status(ctx)
err = ac.SetPower(ctx, true)
err = ac.SetMode(ctx, aircon.Cool)
err = ac.SetTemperature(ctx, 26)
```

高レイヤーはソケットを所有せず、低レイヤーと同じ Client を共有する。
未対応・項目ごとの取得失敗・設定結果不明を保持し、低レイヤーのエラーも参照可能にする。
Status は取得時点の値とし、フィールド代入による暗黙の通信は行わない。
CLI の status・power などは高レイヤーを利用する。

## CLI

```sh
echonet aircon discover --interface eth0
echonet aircon status --host 192.168.1.10 --eoj 013001 --format json
echonet aircon power on --host 192.168.1.10 --eoj 013001
echonet aircon mode cool --host 192.168.1.10 --eoj 013001
echonet aircon temperature 26 --host 192.168.1.10 --eoj 013001
```

操作対象の IP・EOJ を明示し、同一 IP に複数のエアコンがある場合も区別する。
出力は既定で人向け表示、`--format json` で JSON とする。診断は標準エラーへ出す。
終了コードは成功 `0`、引数不正 `2`、通信失敗・不可応答 `1`。
情報の一部が取得できない場合は取得済みの値と項目ごとの理由を表示する。
任意項目の未対応は正常終了、通信失敗・不可応答による欠落は終了コード `1` とする。

## 通信方針

- IPv4 / UDP を使い、3610で待ち受ける。探索先は `224.0.23.0:3610`。
- Get で情報取得、SetC で設定する。応答は相手 IP・TID・EOJ・ESV・EPC で照合する。
- タイムアウトは既定3秒で変更可能。設定要求は自動再送しない。
- 設定応答がない場合は「設定結果不明」とする。成功応答も物理動作の完了を保証しない。
- 設定後の状態は status で確認する。複数設定の一括成功やロールバックは保証しない。
- 不正電文の長さを検証し、使用中の TID を再利用しない。
- コントローラに必要なローカルノード応答を実装する。必須項目は実装前に規格と照合する。

## 実装・検証順序

1. 電文処理・UDP・ローカルノード応答を実装し、仮想機器で検証。
2. エアコン探索と情報取得を実装し、未対応項目・部分失敗を検証。
3. 電源・モード・温度操作を実装し、入力検証・不可応答・タイムアウトを検証。
4. CLI と JSON 出力を実装し、Linux 上の実機で取得・操作・再取得を確認。

受入条件は、対応する実機で状態取得と3種類の操作ができ、未対応・通信失敗を区別できること。
実機試験は明示実行し、通常のテストでは仮想機器を利用する。
Go 1.25、モジュールパスは試作用の `echonet-lite` とする。検証対象のメーカー・型番は未確定。

## 参照仕様

- [ECHONET Lite Ver.1.14](https://echonet.jp/spec_v114_lite/): 電文・サービス・UDP 通信。
- [公式規格一覧](https://echonet.jp/spec_g/): 家庭用エアコンクラスの定義とシステム設計指針。EPC・値の対応と必須／任意を実装前に確認する。

タイムアウトなどの既定値は本プロジェクトの方針であり、規格値ではない。
