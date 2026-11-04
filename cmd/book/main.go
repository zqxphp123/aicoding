// 记账本：本地单机记账服务（Go + SQLite + 内嵌页面）。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/zuoqixiang/accountbook/internal/store"
	"github.com/zuoqixiang/accountbook/internal/web"
)

// version 可在构建时用 -ldflags "-X main.version=..." 覆盖。
var version = "0.1.0"

func main() {
	addr := flag.String("addr", "127.0.0.1:8787", "监听地址；手机访问请用 0.0.0.0:8787")
	dataDir := flag.String("data", "data", "数据目录（数据库、凭证、备份）")
	open := flag.Bool("open", true, "启动后自动打开浏览器")
	flag.Parse()

	log.SetFlags(log.Ltime)
	if err := run(*addr, *dataDir, *open); err != nil {
		log.Fatalf("启动失败: %v", err)
	}
}

func run(addr, dataDir string, open bool) error {
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return fmt.Errorf("创建数据目录: %w", err)
	}

	st, err := store.Open(filepath.Join(abs, "book.db"))
	if err != nil {
		return fmt.Errorf("打开数据库: %w", err)
	}
	defer st.Close()

	srv, err := web.New(st, abs, version)
	if err != nil {
		return fmt.Errorf("初始化: %w", err)
	}

	ln, err := listen(addr)
	if err != nil {
		return fmt.Errorf("监听 %s: %w", addr, err)
	}

	host, port, _ := net.SplitHostPort(ln.Addr().String())
	url := "http://" + net.JoinHostPort(displayHost(host), port)

	log.Printf("记账本 v%s 已启动", version)
	log.Printf("本机访问: %s", url)
	log.Printf("数据目录: %s", abs)
	if isWildcard(host) {
		for _, ip := range localIPs() {
			log.Printf("手机同 Wi-Fi 访问: http://%s", net.JoinHostPort(ip, port))
		}
		log.Printf("提示：手机访问时请保持电脑不休眠，并在防火墙中允许该端口。")
	}

	autoBackup(st, filepath.Join(abs, "backups"))

	httpSrv := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	if open {
		go func() {
			time.Sleep(500 * time.Millisecond)
			if err := openBrowser(url); err != nil {
				log.Printf("未能自动打开浏览器，请手动访问 %s", url)
			}
		}()
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		log.Println("正在退出…")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(ctx)
	}()

	if err := httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	log.Println("已退出")
	return nil
}

// listen 端口被占用时顺延最多 10 个端口。
func listen(addr string) (net.Listener, error) {
	if !strings.Contains(addr, ":") {
		addr += ":8787"
	}
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for i := 0; i < 10 && port+i <= 65535; i++ {
		ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port+i)))
		if err == nil {
			if i > 0 {
				log.Printf("端口 %d 被占用，改用 %d", port, port+i)
			}
			return ln, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func isWildcard(host string) bool {
	return host == "" || host == "0.0.0.0" || host == "::"
}

func displayHost(host string) string {
	if isWildcard(host) {
		return "127.0.0.1"
	}
	return host
}

func localIPs() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipnet.IP.To4()
			if ip4 == nil || ip4.IsLoopback() {
				continue
			}
			out = append(out, ip4.String())
		}
	}
	return out
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}

// autoBackup 每天首次启动时备份一次数据库。
func autoBackup(st *store.Store, dir string) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("创建备份目录失败: %v", err)
		return
	}
	today := time.Now().Format("20060102")
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "book-"+today) {
			return
		}
	}
	p, err := st.Backup(dir)
	if err != nil {
		log.Printf("自动备份失败: %v", err)
		return
	}
	log.Printf("已自动备份: %s", p)
}
