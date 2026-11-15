package sub

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/a923946240/xx-ui/v3/internal/database"
	"github.com/a923946240/xx-ui/v3/internal/database/model"
)

// These are public test keys, deliberately containing '+', '/', and '='.
const (
	shadowrocketServerKey = "+/v7+/v7+/v7+/v7+/v7+/v7+/v7+/v7+/v7+/v7+/s="
	shadowrocketClientKey = "7+/v7+/v7+/v7+/v7+/v7+/v7+/v7+/v7+/v7+/v7+8="
)

// A subscription refresh must preserve the literal SS2022 key chain in
// Shadowrocket, without changing the standard URI returned to other clients.
func TestShadowrocketSubscriptionPreservesSS2022Password(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, encrypted := range []bool{false, true} {
		for _, agent := range []string{"Shadowrocket/2600 CFNetwork/3860 Darwin/25.0.0", "sHaDoWrOcKeT/2600", "v2rayN/7.0", "OtherShadowrocketClient/1.0"} {
			t.Run(fmt.Sprintf("encrypted=%v/%s", encrypted, agent), func(t *testing.T) {
				initSubDB(t)
				ib := seedSubProtocolInbound(t, "ss-ios", "ss", 52336, 1, `{"network":"tcp","security":"none"}`, model.Shadowsocks)
				db := database.GetDB()
				if err := db.Model(ib).Update("settings", fmt.Sprintf(`{"method":"2022-blake3-aes-256-gcm","password":%q,"network":"tcp"}`, shadowrocketServerKey)).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Model(&model.ClientRecord{}).Where("email = ?", "ss@e").Update("password", shadowrocketClientKey).Error; err != nil {
					t.Fatal(err)
				}
				router := gin.New()
				NewSUBController(router.Group("/"), WithSUBEncryption(encrypted))
				req := httptest.NewRequest(http.MethodGet, "/sub/ss-ios", nil)
				req.Header.Set("User-Agent", agent)
				resp := httptest.NewRecorder()
				router.ServeHTTP(resp, req)
				if resp.Code != http.StatusOK {
					t.Fatalf("subscription status = %d", resp.Code)
				}
				body := resp.Body.String()
				if encrypted {
					decoded, err := base64.StdEncoding.DecodeString(body)
					if err != nil {
						t.Fatal(err)
					}
					body = string(decoded)
				}
				line := strings.TrimSpace(body)
				want := "2022-blake3-aes-256-gcm:" + shadowrocketServerKey + ":" + shadowrocketClientKey
				if strings.HasPrefix(strings.ToLower(agent), "shadowrocket/") {
					userinfo, _, ok := strings.Cut(strings.TrimPrefix(line, "ss://"), "@")
					if !ok {
						t.Fatal("missing SS authority")
					}
					decoded, err := base64.RawURLEncoding.DecodeString(userinfo)
					if err != nil {
						t.Fatalf("Shadowrocket userinfo is not Base64URL: %v", err)
					}
					if string(decoded) != want {
						t.Fatalf("decoded test credentials = %q, want %q", decoded, want)
					}
				} else {
					u, err := url.Parse(line)
					if err != nil || u.User == nil {
						t.Fatalf("invalid standard link: %v", err)
					}
					password, _ := u.User.Password()
					if u.User.Username()+":"+password != want || !strings.Contains(line, "%2B") || !strings.Contains(line, "%2F") || !strings.Contains(line, "%3D") {
						t.Fatal("standard SS2022 encoding or credentials changed")
					}
				}
			})
		}
	}
}

func TestShadowrocketSubscriptionLinkKeepsOtherLinkData(t *testing.T) {
	const prefix = "ss://2022-blake3-chacha20-poly1305:server%2B%2F%3D:client%2B%2F%3D"
	const suffix = "@[2001:db8::1]:443/?plugin=obfs-local%3Bobfs%3Dhttp#HK%20%2B%20Node"
	const other = "vless://test-id@example.com:443?type=tcp#VLESS"
	input := prefix + suffix + "\n" + other
	got := shadowrocketSubscriptionLink(input, "Shadowrocket/2600")
	first, second, ok := strings.Cut(got, "\n")
	if !ok || second != other || !strings.HasSuffix(first, suffix) {
		t.Fatal("non-SS link, IPv6 authority, query, or remark changed")
	}
	userinfo := strings.TrimSuffix(strings.TrimPrefix(first, "ss://"), suffix)
	decoded, err := base64.RawURLEncoding.DecodeString(userinfo)
	if err != nil || string(decoded) != "2022-blake3-chacha20-poly1305:server+/=:client+/=" {
		t.Fatalf("test password chain was not preserved: %q, %v", decoded, err)
	}
	if again := shadowrocketSubscriptionLink(got, "Shadowrocket/2600"); again != got {
		t.Fatal("already adapted link changed on a second pass")
	}
}

func TestShadowrocketSubscriptionLinkLeavesUnsupportedInputUntouched(t *testing.T) {
	for _, link := range []string{
		"ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ@example.com:443#legacy",
		"ss://2022-blake3-aes-128-gcm:bad%escape@example.com:443",
		"ss://2022-blake3-aes-128-gcm@example.com:443",
		"ss://2022-blake3-aes-128-gcm:password@",
		"https://example.com/sub/test",
	} {
		if got := shadowrocketSubscriptionLink(link, "Shadowrocket/2600"); got != link {
			t.Fatalf("unsupported test link changed: %q", got)
		}
	}
}
