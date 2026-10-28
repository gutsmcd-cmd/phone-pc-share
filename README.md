# スマホとPCで共有 / Phone PC Share

Send text, the clipboard and files between your phone and your PC over home Wi-Fi.
No cloud, no account, no app store. The phone side is just a web page that the PC program serves.

## 使い方 / How to use
1. PCでプログラムを起動 / Start the program on the PC
   - Windows: download `phone-pc-share-windows.exe` from [Releases](../../releases/latest) and double-click it.
     Windows may say "Windows protected your PC" → **More info → Run anyway** (it isn't code-signed).
     The first time, allow it through the firewall on **Private networks**.
   - Linux: download `phone-pc-share-linux`, `chmod +x phone-pc-share-linux`, run it.
     For the clipboard install `xclip` (X11) or `wl-clipboard` (Wayland).
2. PCのブラウザにQRコードが出ます / A page with a QR code opens in the PC's browser.
3. スマホのカメラでQRを読む / Scan it with the phone camera (same Wi-Fi). Optional: Chrome menu → **Add to Home screen**.

- Phone → PC text is put straight onto the PC clipboard. Files go to `Downloads/PhonePCShare`.
- PC → phone: type/paste text, press **Send PC clipboard**, or choose/drop files; tap **Copy** / **Save** on the phone.
- 日本語 / English switch and 大きい文字 / Large text switch on both sides.

## Privacy
Everything stays on your home network. The phone needs the random pairing key in the QR code;
the key is saved in your user config folder (`phone-pc-share/key`). Traffic is plain HTTP inside your LAN,
so use it on your own home Wi-Fi, not on public Wi-Fi.

## Options
`-port 8765` (default), `-no-browser`, `-key <your key>`

## Build
Go 1.22+: `go build .`  (cross-compile: `GOOS=windows GOARCH=amd64 go build .`)
