//go:build windows

package agent

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// psShotScript — PowerShell-скрипт: снимает КАЖДЫЙ подключённый монитор
// в отдельный PNG и пишет все снимки в один base64-конверт вида
// "monitor|<b64>\nmonitor|<b64>\n...".
const psShotScript = `
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$out = ""
foreach ($s in [System.Windows.Forms.Screen]::AllScreens) {
  $b = $s.Bounds
  $bmp = New-Object System.Drawing.Bitmap($b.Width, $b.Height)
  $g = [System.Drawing.Graphics]::FromImage($bmp)
  $g.CopyFromScreen($b.X, $b.Y, 0, 0, $bmp.Size)
  $ms = New-Object System.IO.MemoryStream
  $bmp.Save($ms, [System.Drawing.Imaging.ImageFormat]::Png)
  $out += "monitor|" + [System.Convert]::ToBase64String($ms.ToArray()) + [char]10
  $g.Dispose(); $bmp.Dispose(); $ms.Dispose()
}
[System.IO.File]::WriteAllText($args[0], $out)
`

// takeScreenshot снимает все экраны по отдельности и склеивает их в один PNG
// горизонтально (виртуальный рабочий стол). Запускается из сессии пользователя —
// у системных сервисов доступа к десктопу нет.
func takeScreenshot() ([]byte, error) {
	tmpDir := os.TempDir()
	psFile := filepath.Join(tmpDir, "swagcore-shot.ps1")
	outFile := filepath.Join(tmpDir, "swagcore-shot.b64")
	if err := os.WriteFile(psFile, []byte(psShotScript), 0o644); err != nil {
		return nil, err
	}
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-File", psFile, outFile)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		os.Remove(psFile)
		os.Remove(outFile)
		return nil, errors.New("powershell: " + err.Error() + ": " + stderr.String())
	}
	raw, err := os.ReadFile(outFile)
	os.Remove(psFile)
	os.Remove(outFile)
	if err != nil {
		return nil, err
	}

	parts, err := parseMonitorEnvelopes(string(raw))
	if err != nil {
		return nil, err
	}
	if len(parts) == 1 {
		return parts[0], nil
	}
	stitched, err := stitchHorizontal(parts)
	if err != nil {
		return nil, err
	}
	return stitched, nil
}

// parseMonitorEnvelopes разбирает конверт "monitor|<b64>\n..." в набор PNG.
func parseMonitorEnvelopes(s string) ([][]byte, error) {
	var pngs [][]byte
	for _, line := range splitLines(s) {
		line = trimSpace(line)
		if line == "" {
			continue
		}
		payload := line
		if i := indexByte(line, '|'); i >= 0 {
			payload = line[i+1:]
		}
		if payload == "" {
			continue
		}
		data, err := decodeB64(payload)
		if err != nil {
			return nil, fmt.Errorf("shot b64: %w", err)
		}
		if len(data) > 8 {
			pngs = append(pngs, data)
		}
	}
	if len(pngs) == 0 {
		return nil, errors.New("no monitors captured")
	}
	return pngs, nil
}
