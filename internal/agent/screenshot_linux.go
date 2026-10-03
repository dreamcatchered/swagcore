//go:build linux

package agent

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// takeScreenshot на Linux. Возможны три случая:
//  1. X11-сессия → scrot / import / gnome-screenshot / xwd;
//  2. Wayland (GNOME) → gnome-screenshot (через DBus) или grim (sway/wlroots);
//  3. headless VPS без дисплея → честная ошибка: скриншота физически нет.
//
// Сервис агента наследует окружение пользовательской сессии через displayEnv()
// (DISPLAY/XAUTHORITY/DBUS), поэтому скриншот работает и из systemd-службы,
// когда пользователь залогинен.
func takeScreenshot() ([]byte, error) {
	if isWayland() {
		if data, err := shotWayland(); err == nil {
			return data, nil
		}
	}
	// X11-инструменты (или gnome-screenshot, который сам разберётся)
	candidates := []struct {
		bin  string
		args []string
	}{
		{"scrot", []string{"-o", "/tmp/swagcore-shot.png"}},
		{"gnome-screenshot", []string{"-f", "/tmp/swagcore-shot.png"}},
		{"import", []string{"-window", "root", "/tmp/swagcore-shot.png"}},
		{"maim", []string{"/tmp/swagcore-shot.png"}},
	}
	var lastErr error
	for _, c := range candidates {
		if _, err := exec.LookPath(c.bin); err != nil {
			continue
		}
		tmp := filepath.Join("/tmp", "swagcore-shot-"+c.bin+".png")
		args := c.args
		if len(args) > 0 {
			args[len(args)-1] = tmp
		}
		cmd := exec.Command(c.bin, args...)
		cmd.Env = displayEnv()
		if err := cmd.Run(); err != nil {
			lastErr = fmt.Errorf("%s: %v", c.bin, err)
			continue
		}
		if data, err := os.ReadFile(tmp); err == nil && len(data) > 8 {
			_ = os.Remove(tmp)
			return data, nil
		}
		lastErr = fmt.Errorf("%s: пустой вывод", c.bin)
	}
	if lastErr == nil {
		lastErr = errors.New("не найдено ни одной утилиты скриншота (scrot/gnome-screenshot/import/maim)")
	}
	// Headless-VPS (нет дисплея) — делаем честный информационный снимок состояния:
	// hostname, аптайм, load, память, топ CPU-процессов. Это лучше, чем ошибка.
	if !hasDisplay() {
		return headlessInfoShot(), nil
	}
	return nil, fmt.Errorf(
		"%v; DISPLAY есть, но утилиты скриншота нет — поставьте: apt install scrot",
		lastErr)
}

// hasDisplay проверяет, есть ли X/Wayland-сессия на машине.
func hasDisplay() bool {
	if os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != "" {
		return true
	}
	// сессии других пользователей: /tmp/.X11-unix/X*
	if ents, err := os.ReadDir("/tmp/.X11-unix"); err == nil && len(ents) > 0 {
		return true
	}
	return false
}

// headlessInfoShot рисует PNG-карточку с состоянием ноды (для VPS без дисплея).
func headlessInfoShot() []byte {
	const W, H, pad = 900, 560, 36
	canvas := image.NewRGBA(image.Rect(0, 0, W, H))
	bg := color.RGBA{15, 17, 21, 255}
	fg := color.RGBA{230, 233, 240, 255}
	accent := color.RGBA{91, 140, 255, 255}
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{bg}, image.Point{}, draw.Src)

	host, _ := os.Hostname()
	lines := []string{
		"swagCore — headless node snapshot",
		"",
		"host:    " + host + "  (" + runtime.GOOS + "/" + runtime.GOARCH + ")",
		"time:    " + time.Now().Format("2006-01-02 15:04:05 MST"),
		"uptime:  " + uptimeHuman(),
		"load:    " + loadAvg(),
		"memory:  " + memLine(),
		"user:    " + currentUser(),
		"",
		"дисплея на машине нет — это VPS без графической сессии,",
		"поэтому вместо экрана — карточка состояния ноды.",
	}
	// Простая отрисовка текста стандартным шрифтом (golang.org/x/image не нужен).
	drawText := func(y int, s string, c color.RGBA) {
		basicDrawString(canvas, 24, y, s, c)
	}
	y := pad + 18
	for i, ln := range lines {
		c := fg
		if i == 0 {
			c = accent
		}
		drawText(y, ln, c)
		y += 34
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, canvas)
	return buf.Bytes()
}

func uptimeHuman() string {
	if uptimeSec() == 0 {
		return "n/a"
	}
	d := time.Duration(uptimeSec()) * time.Second
	return d.Truncate(time.Second).String()
}

func loadAvg() string {
	raw, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return "n/a"
	}
	f := strings.Fields(string(raw))
	if len(f) < 3 {
		return "n/a"
	}
	return f[0] + " " + f[1] + " " + f[2]
}

func memLine() string {
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return "n/a"
	}
	var total, avail uint64
	for _, ln := range strings.Split(string(raw), "\n") {
		f := strings.Fields(ln)
		if len(f) < 2 {
			continue
		}
		v, _ := strconv.ParseUint(f[1], 10, 64)
		switch f[0] {
		case "MemTotal:":
			total = v / 1024
		case "MemAvailable:":
			avail = v / 1024
		}
	}
	if total == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%d/%d MB used", total-avail, total)
}

func currentUser() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return "n/a"
}

// isWayland определяет Wayland-сессию.
func isWayland() bool {
	if strings.Contains(strings.ToLower(os.Getenv("XDG_SESSION_TYPE")), "wayland") {
		return true
	}
	return os.Getenv("WAYLAND_DISPLAY") != ""
}

// shotWayland: grim (wlroots/sway) или gnome-screenshot (DBus, GNOME Wayland).
func shotWayland() ([]byte, error) {
	if _, err := exec.LookPath("grim"); err == nil {
		tmp := "/tmp/swagcore-shot-grim.png"
		cmd := exec.Command("grim", tmp)
		cmd.Env = displayEnv()
		if err := cmd.Run(); err == nil {
			if data, err := os.ReadFile(tmp); err == nil {
				_ = os.Remove(tmp)
				return data, nil
			}
		}
	}
	if _, err := exec.LookPath("gnome-screenshot"); err == nil {
		tmp := "/tmp/swagcore-shot-gnome.png"
		cmd := exec.Command("gnome-screenshot", "-f", tmp)
		cmd.Env = displayEnv()
		if err := cmd.Run(); err == nil {
			if data, err := os.ReadFile(tmp); err == nil {
				_ = os.Remove(tmp)
				return data, nil
			}
		}
	}
	return nil, errors.New("no wayland screenshot tool (need grim or gnome-screenshot)")
}
