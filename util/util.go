package util

import (
   "os"
   "os/exec"
   "runtime"
   "strings"

   "github.com/mattn/go-tty"
)

const (
	TermMaxWidth        = 100
	TermSafeZonePadding = 10
)

func StartsWithCodeBlock(s string) bool {
	if len(s) <= 3 {
		return strings.Repeat("`", len(s)) == s
	}
	return strings.HasPrefix(s, "```")
}

func ExtractFirstCodeBlock(s string) (content string, isOnlyCode bool) {
	isOnlyCode = true
	if len(s) <= 3 {
		return "", false
	}
	start := strings.Index(s, "```")
	if start == -1 {
		return "", false
	}
	if start != 0 {
		isOnlyCode = false
	}
	fromStart := s[start:]
	content = strings.TrimPrefix(fromStart, "```")
	// Find newline after the first ```
	newlinePos := strings.Index(content, "\n")
	if newlinePos != -1 {
		// Check if there's a word immediately after the first ```
		if content[0:newlinePos] == strings.TrimSpace(content[0:newlinePos]) {
			// If so, remove that part from the content
			content = content[newlinePos+1:]
		}
	}
	// Strip final ``` if present
	end := strings.Index(content, "```")
	if end < len(content)-3 {
		isOnlyCode = false
	}
	if end != -1 {
		content = content[:end]
	}
	if len(content) == 0 {
		return "", false
	}
	// Strip the final newline, if present
	if content[len(content)-1] == '\n' {
		content = content[:len(content)-1]
	}
	return
}

func GetTermSafeMaxWidth() int {
   termWidth, err := getTermWidth()
   if err != nil {
       return TermMaxWidth
   }
   width := termWidth - TermSafeZonePadding
   if width <= 0 {
       return termWidth
   }
   return width
}

func getTermWidth() (width int, err error) {
	t, err := tty.Open()
	if err != nil {
		return 0, err
	}
	defer t.Close()
	width, _, err = t.Size()
	return width, err
}

func IsLikelyBillingError(s string) bool {
	return strings.Contains(s, "429 Too Many Requests")
}

func OpenBrowser(url string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default: // For Linux or anything else
		cmd = exec.Command("xdg-open", url)
	}

	return cmd.Start()
}
// GetOSInfo returns a user-friendly OS name and version (e.g., "Ubuntu 22.04" or "Windows 10").
func GetOSInfo() string {
   osName := runtime.GOOS + "/" + runtime.GOARCH
   switch runtime.GOOS {
   case "linux":
       data, err := os.ReadFile("/etc/os-release")
       if err == nil {
           for _, line := range strings.Split(string(data), "\n") {
               if strings.HasPrefix(line, "PRETTY_NAME=") {
                   val := strings.Trim(line[len("PRETTY_NAME="):], "\"")
                   return val
               }
           }
       }
   case "darwin":
       name, err1 := exec.Command("sw_vers", "-productName").Output()
       ver, err2 := exec.Command("sw_vers", "-productVersion").Output()
       if err1 == nil && err2 == nil {
           return strings.TrimSpace(string(name)) + " " + strings.TrimSpace(string(ver))
       }
   case "windows":
       out, err := exec.Command("powershell", "-NoProfile", "-Command", "(Get-CimInstance Win32_OperatingSystem).Caption + ' ' + (Get-CimInstance Win32_OperatingSystem).Version").Output()
       if err == nil {
           return strings.TrimSpace(string(out))
       }
   }
   return osName
}
