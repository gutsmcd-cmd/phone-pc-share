// Phone PC Share (スマホとPCで共有): send text, clipboard and files between phone and PC
// over home Wi-Fi. No cloud, no accounts. The phone just opens a web page served by this app.
package main

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/atotto/clipboard"
	qrcode "github.com/skip2/go-qrcode"
)

//go:embed web/*
var webFS embed.FS

type Item struct {
	ID   int    `json:"id"`
	From string `json:"from"` // "pc" or "phone"
	Kind string `json:"kind"` // "text" or "file"
	Text string `json:"text,omitempty"`
	Name string `json:"name,omitempty"`
	Size int64  `json:"size,omitempty"`
	Time int64  `json:"time"`
	path string
}

var (
	mu      sync.Mutex
	items   []*Item
	nextID  = 1
	token   string
	saveDir string
	port    int
	autoCopy = true
)

func add(it *Item) *Item {
	mu.Lock()
	defer mu.Unlock()
	it.ID = nextID
	nextID++
	it.Time = time.Now().UnixMilli()
	items = append(items, it)
	if len(items) > 200 {
		items = items[len(items)-200:]
	}
	return it
}

func isLocal(r *http.Request) bool {
	h, _, _ := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func role(r *http.Request) string {
	if isLocal(r) && r.URL.Query().Get("as") != "phone" {
		return "pc"
	}
	return "phone"
}

// auth: localhost is trusted; others need the pairing key (from the QR code).
func auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		k := r.Header.Get("X-Key")
		if k == "" {
			k = r.URL.Query().Get("k")
		}
		if !isLocal(r) && k != token {
			http.Error(w, "pairing key required / ペアリングキーが違います", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func lanIPs() []string {
	var out []string
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && n.IP.IsPrivate() {
				out = append(out, n.IP.String())
			}
		}
	}
	return out
}

func phoneURL() string {
	ips := lanIPs()
	ip := "127.0.0.1"
	if len(ips) > 0 {
		ip = ips[0]
	}
	return fmt.Sprintf("http://%s:%d/?k=%s", ip, port, token)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

func safeName(n string) string {
	n = filepath.Base(strings.ReplaceAll(n, "\\", "/"))
	n = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`<>:"/\|?*`, r) || r < 32 {
			return '_'
		}
		return r
	}, n)
	if n == "" || n == "." || n == ".." {
		n = "file"
	}
	return n
}

func uniquePath(dir, name string) string {
	p := filepath.Join(dir, name)
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 2; ; i++ {
		if _, err := os.Stat(p); os.IsNotExist(err) {
			return p
		}
		p = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", base, i, ext))
	}
}

func main() {
	flag.IntVar(&port, "port", 8765, "port")
	noOpen := flag.Bool("no-browser", false, "don't open the PC page in a browser")
	flag.StringVar(&token, "key", "", "pairing key (default: saved random key)")
	flag.Parse()

	home, _ := os.UserHomeDir()
	saveDir = filepath.Join(home, "Downloads", "PhonePCShare")
	os.MkdirAll(saveDir, 0o755)
	cfgDir, _ := os.UserConfigDir()
	keyFile := filepath.Join(cfgDir, "phone-pc-share", "key")
	if token == "" {
		if b, err := os.ReadFile(keyFile); err == nil && len(strings.TrimSpace(string(b))) >= 8 {
			token = strings.TrimSpace(string(b))
		} else {
			b := make([]byte, 6)
			rand.Read(b)
			token = hex.EncodeToString(b)
			os.MkdirAll(filepath.Dir(keyFile), 0o700)
			os.WriteFile(keyFile, []byte(token), 0o600)
		}
	}
	tmpDir, _ := os.MkdirTemp("", "phone-pc-share-")

	sub := http.FileServer(http.FS(webFS))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			r.URL.Path = "/web/index.html"
		} else if !strings.HasPrefix(r.URL.Path, "/web/") {
			r.URL.Path = "/web" + r.URL.Path
		}
		sub.ServeHTTP(w, r)
	})
	http.HandleFunc("/api/info", auth(func(w http.ResponseWriter, r *http.Request) {
		host, _ := os.Hostname()
		writeJSON(w, map[string]any{"role": role(r), "host": host, "url": phoneURL(), "ips": lanIPs(), "port": port, "saveDir": saveDir, "autoCopy": autoCopy})
	}))
	http.HandleFunc("/api/qr.png", auth(func(w http.ResponseWriter, r *http.Request) {
		png, _ := qrcode.Encode(phoneURL(), qrcode.Medium, 320)
		w.Header().Set("Content-Type", "image/png")
		w.Write(png)
	}))
	http.HandleFunc("/api/items", auth(func(w http.ResponseWriter, r *http.Request) {
		since, _ := strconv.Atoi(r.URL.Query().Get("since"))
		mu.Lock()
		out := []*Item{}
		for _, it := range items {
			if it.ID > since {
				out = append(out, it)
			}
		}
		mu.Unlock()
		writeJSON(w, out)
	}))
	http.HandleFunc("/api/text", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST", 405)
			return
		}
		b, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var in struct{ Text string }
		if json.Unmarshal(b, &in) != nil || strings.TrimSpace(in.Text) == "" {
			http.Error(w, "empty", 400)
			return
		}
		from := role(r)
		it := add(&Item{From: from, Kind: "text", Text: in.Text})
		if from == "phone" && autoCopy {
			clipboard.WriteAll(in.Text)
		}
		writeJSON(w, it)
	}))
	http.HandleFunc("/api/pc-clipboard", auth(func(w http.ResponseWriter, r *http.Request) {
		if role(r) != "pc" {
			http.Error(w, "PC only", 403)
			return
		}
		t, err := clipboard.ReadAll()
		if err != nil || strings.TrimSpace(t) == "" {
			http.Error(w, "PC clipboard empty or unavailable", 409)
			return
		}
		writeJSON(w, add(&Item{From: "pc", Kind: "text", Text: t}))
	}))
	http.HandleFunc("/api/copy", auth(func(w http.ResponseWriter, r *http.Request) { // copy an item to the PC clipboard
		id, _ := strconv.Atoi(r.URL.Query().Get("id"))
		mu.Lock()
		var txt string
		for _, it := range items {
			if it.ID == id {
				txt = it.Text
			}
		}
		mu.Unlock()
		if err := clipboard.WriteAll(txt); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, "ok")
	}))
	http.HandleFunc("/api/file", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST", 405)
			return
		}
		mr, err := r.MultipartReader()
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		from := role(r)
		var added []*Item
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			if p.FileName() == "" {
				continue
			}
			dir := saveDir
			if from == "pc" {
				dir = tmpDir
			}
			dst := uniquePath(dir, safeName(p.FileName()))
			f, err := os.Create(dst)
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			n, err := io.Copy(f, p)
			f.Close()
			if err != nil {
				os.Remove(dst)
				http.Error(w, err.Error(), 500)
				return
			}
			added = append(added, add(&Item{From: from, Kind: "file", Name: filepath.Base(dst), Size: n, path: dst}))
		}
		writeJSON(w, added)
	}))
	http.HandleFunc("/api/dl", auth(func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.Atoi(r.URL.Query().Get("id"))
		mu.Lock()
		var it *Item
		for _, x := range items {
			if x.ID == id && x.Kind == "file" {
				it = x
			}
		}
		mu.Unlock()
		if it == nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+urlEsc(it.Name))
		http.ServeFile(w, r, it.path)
	}))
	http.HandleFunc("/api/open-folder", auth(func(w http.ResponseWriter, r *http.Request) {
		if isLocal(r) {
			openThing(saveDir)
		}
		writeJSON(w, "ok")
	}))

	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		log.Fatalf("Port %d busy (already running?): %v", port, err)
	}
	fmt.Println("Phone PC Share / スマホとPCで共有")
	fmt.Println("PC page:     http://127.0.0.1:" + strconv.Itoa(port) + "/")
	fmt.Println("Phone page:  " + phoneURL())
	fmt.Println("Files from the phone are saved in: " + saveDir)
	fmt.Println("Close this window to stop. / このウィンドウを閉じると終了します。")
	if !*noOpen {
		go func() { time.Sleep(400 * time.Millisecond); openThing("http://127.0.0.1:" + strconv.Itoa(port) + "/") }()
	}
	log.Fatal(http.Serve(ln, nil))
}

func urlEsc(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || strings.IndexByte("-._~", c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func openThing(target string) {
	switch runtime.GOOS {
	case "windows":
		exec.Command("rundll32", "url.dll,FileProtocolHandler", target).Start()
	case "darwin":
		exec.Command("open", target).Start()
	default:
		exec.Command("xdg-open", target).Start()
	}
}
