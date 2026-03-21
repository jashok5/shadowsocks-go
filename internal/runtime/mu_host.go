package runtime

import (
	"crypto/md5"
	"encoding/hex"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jashok5/shadowsocks-go/internal/model"
)

var muTokenPattern = regexp.MustCompile(`%-?[1-9]\d*m`)

func getMUHost(regexText string, suffix string, user model.User) string {
	regexText = strings.TrimSpace(regexText)
	if regexText == "" {
		regexText = "%5m%id.%suffix"
	}
	text := strings.ReplaceAll(regexText, "%id", strconv.Itoa(user.ID))
	text = strings.ReplaceAll(text, "%suffix", strings.TrimSpace(suffix))

	sum := md5.Sum([]byte(strconv.Itoa(user.ID) + user.Passwd + user.Method + user.Obfs + user.Protocol))
	hash := hex.EncodeToString(sum[:])

	tokens := muTokenPattern.FindAllString(text, -1)
	for _, token := range tokens {
		n := strings.TrimSuffix(strings.TrimPrefix(token, "%"), "m")
		count, err := strconv.Atoi(n)
		if err != nil || count == 0 {
			continue
		}
		repl := ""
		if count < 0 {
			idx := len(hash) + count
			if idx < 0 {
				idx = 0
			}
			repl = hash[idx:]
		} else {
			if count > len(hash) {
				count = len(hash)
			}
			repl = hash[:count]
		}
		text = strings.Replace(text, token, repl, 1)
	}
	return text
}

func buildMUHostMap(users []model.User, rule MUHostRule) map[int]string {
	out := make(map[int]string)
	if !rule.Enabled {
		return out
	}
	for _, u := range users {
		if u.IsMultiUser != 0 {
			continue
		}
		host := strings.TrimSpace(getMUHost(rule.Regex, rule.Suffix, u))
		if host == "" {
			continue
		}
		out[u.ID] = host
	}
	return out
}

func collectMUHosts(muHostMap map[int]string) []string {
	if len(muHostMap) == 0 {
		return nil
	}
	uniq := make(map[string]struct{}, len(muHostMap))
	for _, host := range muHostMap {
		if strings.TrimSpace(host) == "" {
			continue
		}
		uniq[host] = struct{}{}
	}
	out := make([]string, 0, len(uniq))
	for h := range uniq {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}
