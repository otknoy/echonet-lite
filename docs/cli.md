# CLI と設定

基本操作は [README](../README.md) を参照。

## 初期設定・対象切り替え

| コマンド | 動作 |
|---|---|
| `echonet config init` | NIC 選択 → 探索 → 各エアコンの登録可否・命名 → 操作対象選択 |
| `echonet config init --force` | 対話完了後に既存設定を置き換える |
| `echonet config init --empty` | 探索せず空のひな形を作成する |
| `echonet config list` | 対象一覧を表示。選択中の対象に `*` |
| `echonet config use NAME` | 操作対象を切り替えて保存する |

init の選択は番号または名前で入力する。登録なしの場合は NIC のみ保存する。
入力中断・探索失敗時は保存しない。再作成時も元の設定を保持する。
探索時間は `config init --timeout 5s` のように変更できる。

## 設定ファイル

既定パスは `~/.config/echonet/config.yaml`。
`XDG_CONFIG_HOME` が設定されていれば、その配下の `echonet/config.yaml` を使う。
各コマンドの末尾に `--config PATH` を付けると別のファイルを利用できる。

[config.example.yaml](../config.example.yaml) の項目:

| 項目 | 内容 |
|---|---|
| `interface` | 共通の NIC 名。空欄なら候補が一つの場合に自動選択 |
| `targets` | 任意の名前と、host・EOJ の組 |
| `current_target` | 操作対象の名前 |

host は IPv4 アドレス、EOJ は機器オブジェクトを識別する6桁の16進文字列（例: `"013001"`）。
同じ IP 上の複数機器も EOJ で区別する。

## 引数での指定

```sh
echonet aircon discover --interface eth0
echonet aircon status --interface eth0 --host 192.168.1.10 --eoj 013001
```

| オプション | 動作 |
|---|---|
| `--interface NAME` | 設定の NIC を上書き |
| `--host IP --eoj EOJ` | 今回だけ操作対象を上書き。両方の指定が必要 |
| `--timeout 3s` | 要求・探索の待機時間。既定3秒 |
| `--format text\|json` | 出力形式。既定 text |
| `--config PATH` | 使用する設定ファイル |

これらは aircon コマンドで利用できる。discover は対象選択を使わず、host・EOJ を受け付けない。
設定ファイルがなければ引数だけで動作する。明示指定した設定ファイルがない場合や設定が不正な場合はエラー。

## 結果の読み方

- status の JSON は各項目に `value`・`state` を持つ。state は `ok`・`unsupported`・`error`、値がなければ `null`。
- 設定成功の `accepted` は要求の受理を示す。設定後の値は status で確認する。応答がない場合は「設定結果不明」とし、再送しない。
- 診断は標準エラーに出力する。取得が部分失敗しても取得済みの値を出力する。
- 終了コードは成功 `0`、処理・通信失敗 `1`、引数・設定不正 `2`。未対応項目のみの場合や探索結果が空の場合は正常終了。
