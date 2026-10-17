# echonet-lite

ECHONET Lite 対応エアコンの状態取得・操作を行う Go ライブラリと CLI の試作です。
Linux・Go 1.25 以降に対応しています。

## インストール

```sh
go install github.com/otknoy/echonet-lite/cmd/echonet@latest
```

## 初期設定

エアコンの ECHONET Lite 機能を有効にし、同じ LAN で実行してください。

```sh
echonet config init
```

ネットワークインターフェースを選び、見つかったエアコンに名前を付けて登録し、操作対象を選択します。
設定は `~/.config/echonet/config.yaml` に保存されます。
作り直す場合は `echonet config init --force` を使います。

## 使い方

```sh
echonet aircon status          # 電源・運転モード・設定温度・室温を取得
echonet aircon power on        # 電源 ON（OFF は off）
echonet aircon mode cool       # 冷房に変更
echonet aircon temperature 26  # 設定温度を26℃に変更
```

モードは `auto`・`cool`・`heat`・`dry`・`fan` です。対応する操作・温度範囲は機器によって異なります。
JSON 出力は `echonet aircon status --format json` で取得できます。

複数のエアコンを切り替える場合:

```sh
echonet config list
echonet config use bedroom
```

詳しくは [CLI と設定](docs/cli.md)、[ライブラリの使い方](docs/library.md)、[対応範囲・検証](docs/verification.md)を参照してください。
