package auth

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/models"
	"gorm.io/gorm"
)

func protocolBody(extra string) string {
	return strings.TrimSuffix(exchangeBody, "}") + "," + extra + "}"
}
func assertNoSub2APIProvisioning(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, model := range []any{&models.User{}, &models.UserCredential{}, &models.Sub2APIIdentity{}, &models.LLMUserProvider{}, &models.UserSession{}} {
		var count int64
		if err := db.Model(model).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("unexpected provisioning %T count=%d err=%v", model, count, err)
		}
	}
}
func TestSub2APIHTTPProtocolTwoPhaseAndRememberedIdempotency(t *testing.T) {
	for _, protocol := range []string{"openai_responses", "anthropic_messages", "openai_chat_completions"} {
		t.Run(protocol, func(t *testing.T) {
			router, db, remote := exchangeFixture(t, true)
			remote.snapshot = multiSnapshot(1)
			remote.snapshot.Keys[0].Group.Platform = "unknown\x00\u202e" + strings.Repeat("界", 200)
			remote.snapshot.Keys[0].Name = "名称\n" + strings.Repeat("名", 200)
			remote.snapshot.Keys[0].Group.Name = "分组\t" + strings.Repeat("组", 200)
			w := exchangeRequest(router, exchangeBody)
			if w.Code != 409 {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			assertNoSub2APIProvisioning(t, db)
			assertSub2APIExpiredCookie(t, w)
			var response struct {
				ErrorCode string `json:"errorCode"`
				ErrorMsg  string `json:"errorMsg"`
				Details   struct {
					Selection struct {
						Items []struct {
							KeyID     int64  `json:"keyID"`
							Name      string `json:"name"`
							GroupName string `json:"groupName"`
							Platform  string `json:"platform"`
						} `json:"items"`
						Protocols []string `json:"protocols"`
					} `json:"protocolSelection"`
				} `json:"details"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			selection := response.Details.Selection
			if response.ErrorCode != "auth.sub2api_protocol_required" || response.ErrorMsg != "select a protocol for unrecognized sub2api platforms" || len(selection.Items) != 1 || selection.Items[0].KeyID != 1 || strings.Join(selection.Protocols, ",") != "openai_responses,anthropic_messages,openai_chat_completions" {
				t.Fatalf("contract=%s", w.Body.String())
			}
			for _, value := range []string{selection.Items[0].Name, selection.Items[0].GroupName, selection.Items[0].Platform} {
				if len([]rune(value)) > 128 || strings.IndexFunc(value, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) >= 0 {
					t.Fatal("unsafe display field")
				}
			}
			for _, secret := range []string{"synthetic-key-", "synthetic-login-token", "owner@example.com", "远端昵称", "accessToken", "providerID"} {
				if strings.Contains(w.Body.String(), secret) {
					t.Fatal("private data disclosed", secret)
				}
			}
			chosen := protocolBody(fmt.Sprintf(`"protocolSelections":[{"keyID":1,"protocol":%q}]`, protocol))
			w = exchangeRequest(router, chosen)
			if w.Code != 200 || decodeExchange(t, w).Sub2APIImport.Imported != 1 {
				t.Fatalf("choice=%d %s", w.Code, w.Body.String())
			}
			first := decodeExchange(t, w)
			var provider models.LLMUserProvider
			db.First(&provider)
			if provider.Protocol != protocol || provider.BaseURL != "https://sub.example.com/v1" {
				t.Fatal("wrong candidate persisted")
			}
			// A subsequent exchange supplies no choices and reads the persisted protocol.
			w = exchangeRequest(router, exchangeBody)
			if w.Code != 200 || decodeExchange(t, w).Sub2APIImport.Reused != 1 || decodeExchange(t, w).User.ID != first.User.ID {
				t.Fatalf("reuse=%d %s", w.Code, w.Body.String())
			}
			var count int64
			db.Model(&models.LLMUserProvider{}).Count(&count)
			if count != 1 {
				t.Fatal("duplicate import")
			}
			// Supplying a different protocol cannot mutate an existing credential.
			other := "openai_responses"
			if protocol == other {
				other = "anthropic_messages"
			}
			w = exchangeRequest(router, protocolBody(fmt.Sprintf(`"protocolSelections":[{"keyID":1,"protocol":%q}]`, other)))
			if w.Code != 403 || !strings.Contains(w.Body.String(), "credential_changed") {
				t.Fatalf("override=%d %s", w.Code, w.Body.String())
			}
			db.First(&provider)
			if provider.Protocol != protocol {
				t.Fatal("stored protocol changed")
			}
		})
	}
}

func TestSub2APIHTTPRememberedChoiceSurvivesKnownPlatformDefaultsAndChanges(t *testing.T) {
	for _, tc := range []struct{ before, after, chosen string }{
		{"openai", "openai", "anthropic_messages"},
		{"deepseek", "deepseek", "openai_chat_completions"},
		{"gemini", "gemini", "openai_responses"},
		{"unknown", "deepseek", "anthropic_messages"},
	} {
		t.Run(tc.before+"-"+tc.after+"-"+tc.chosen, func(t *testing.T) {
			router, db, remote := exchangeFixture(t, true)
			remote.snapshot = multiSnapshot(1)
			remote.snapshot.Keys[0].Group.Platform = tc.before
			chosen := protocolBody(fmt.Sprintf(`"protocolSelections":[{"keyID":1,"protocol":%q}]`, tc.chosen))
			w := exchangeRequest(router, chosen)
			if w.Code != 200 {
				t.Fatalf("initial choice: %d %s", w.Code, w.Body.String())
			}
			first := decodeExchange(t, w)
			remote.snapshot.Keys[0].Group.Platform = tc.after
			w = exchangeRequest(router, exchangeBody)
			if w.Code != 200 || decodeExchange(t, w).Sub2APIImport.Reused != 1 || decodeExchange(t, w).User.ID != first.User.ID {
				t.Fatalf("remembered choice: %d %s", w.Code, w.Body.String())
			}
			var rows []models.LLMUserProvider
			if err := db.Find(&rows).Error; err != nil || len(rows) != 1 || rows[0].Protocol != tc.chosen {
				t.Fatal("stored choice was duplicated or overwritten", err)
			}
		})
	}
}

func TestSub2APIHTTPProtocolMixedRemainingSkipAndRevalidation(t *testing.T) {
	router, db, remote := exchangeFixtureWithLimit(t, true, exchangeAllowLimiter{}, 5)
	remote.snapshot = multiSnapshot(3)
	remote.snapshot.Keys[1].Group.Platform = "unknown-a"
	remote.snapshot.Keys[2].Group.Platform = "unknown-b"
	w := exchangeRequest(router, exchangeBody)
	if w.Code != 409 {
		t.Fatal(w.Body.String())
	}
	assertNoSub2APIProvisioning(t, db)
	one := protocolBody(`"protocolSelections":[{"keyID":2,"protocol":"anthropic_messages"}]`)
	w = exchangeRequest(router, one)
	if w.Code != 409 || strings.Contains(w.Body.String(), `"keyID":2`) || !strings.Contains(w.Body.String(), `"keyID":3`) {
		t.Fatalf("remaining=%s", w.Body.String())
	}
	assertNoSub2APIProvisioning(t, db)
	// Re-fetching ownership on phase two rejects a changed remote snapshot.
	remote.snapshot.Keys[2].UserID = 999
	w = exchangeRequest(router, protocolBody(`"protocolSelections":[{"keyID":2,"protocol":"anthropic_messages"}],"skipKeyIDs":[3]`))
	if w.Code != 401 {
		t.Fatal("ownership not revalidated", w.Code)
	}
	assertNoSub2APIProvisioning(t, db)
	remote.snapshot.Keys[2].UserID = 7
	w = exchangeRequest(router, protocolBody(`"protocolSelections":[{"keyID":2,"protocol":"anthropic_messages"}],"skipKeyIDs":[3]`))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	summary := decodeExchange(t, w).Sub2APIImport
	if summary.Imported != 2 || summary.Skipped != 1 || summary.Items[2].Reason != "user_skipped" {
		t.Fatalf("summary=%+v", summary)
	}
	// Skips are request-local, so the unimported third key is asked again.
	w = exchangeRequest(router, exchangeBody)
	if w.Code != 409 || strings.Contains(w.Body.String(), `"keyID":2`) || !strings.Contains(w.Body.String(), `"keyID":3`) {
		t.Fatal(w.Body.String())
	}
	assertSub2APIExpiredCookie(t, w)
}

func TestSub2APIHTTPProtocolRejectsInvalidSelectionsBeforeWrites(t *testing.T) {
	router, db, remote := exchangeFixture(t, true)
	remote.snapshot = multiSnapshot(1)
	remote.snapshot.Keys[0].Group.Platform = "unknown"
	for _, extra := range []string{
		`"protocolSelections":[{"keyID":1,"protocol":"google_generate_content"}]`,
		`"protocolSelections":[{"keyID":1,"protocol":"arbitrary"}]`,
		`"protocolSelections":[{"keyID":0,"protocol":"openai_responses"}]`,
		`"protocolSelections":[{"keyID":9007199254740992,"protocol":"openai_responses"}]`,
		`"protocolSelections":[{"keyID":1.5,"protocol":"openai_responses"}]`,
		`"protocolSelections":[{"keyID":1,"protocol":"openai_responses","baseURL":"https://evil.example.com"}]`,
		`"protocolSelections":[{"keyID":1,"protocol":"openai_responses"},{"keyID":1,"protocol":"anthropic_messages"}]`,
		`"protocolSelections":[{"keyID":2,"protocol":"openai_responses"}]`,
		`"protocolSelections":[{"keyID":1,"protocol":"openai_responses"}],"skipKeyIDs":[1]`,
		`"skipKeyIDs":[1,1]`, `"skipKeyIDs":[-1]`, `"skipKeyIDs":[9007199254740992]`, `"skipKeyIDs":[2]`,
	} {
		w := exchangeRequest(router, protocolBody(extra))
		if w.Code != 400 {
			t.Fatalf("invalid selection status=%d", w.Code)
		}
		assertNoSub2APIProvisioning(t, db)
	}
	for _, mutate := range []func(){
		func() { remote.snapshot.Keys[0].UserID = 999 },
		func() {
			remote.snapshot.Keys[0].UserID = 7
			remote.snapshot.Keys = append(remote.snapshot.Keys, remote.snapshot.Keys[0])
		},
	} {
		mutate()
		w := exchangeRequest(router, exchangeBody)
		if w.Code == 409 || w.Code == 200 {
			t.Fatal("invalid complete list accepted")
		}
		assertNoSub2APIProvisioning(t, db)
	}
}

func TestSub2APIHTTPProtocolRequestBounds(t *testing.T) {
	router, db, remote := exchangeFixture(t, true)
	remote.snapshot = multiSnapshot(2000)
	choices := make([]string, 2000)
	for i := range choices {
		choices[i] = fmt.Sprintf(`{"keyID":%d,"protocol":"openai_responses"}`, i+1)
		remote.snapshot.Keys[i].Status = "disabled"
	}
	body := `{"userID":7,"token":"` + strings.Repeat("x", 16384) + `","protocolSelections":[` + strings.Join(choices, ",") + `]}`
	w := exchangeRequest(router, body)
	if w.Code != 403 {
		t.Fatalf("valid bounded request=%d %s", w.Code, w.Body.String())
	}
	body = strings.TrimSuffix(body, "}") + `,"skipKeyIDs":[2001]}`
	if w = exchangeRequest(router, body); w.Code != 400 {
		t.Fatal("combined entry limit", w.Code)
	}
	if w = exchangeRequest(router, `{"userID":7,"token":"`+strings.Repeat("x", 16385)+`"}`); w.Code != 400 {
		t.Fatal("token limit", w.Code)
	}
	if w = exchangeRequest(router, exchangeBody+strings.Repeat(" ", 256<<10)); w.Code != 413 {
		t.Fatal("total body cap", w.Code)
	}
	assertNoSub2APIProvisioning(t, db)
}

func TestSub2APIHTTPProtocolChoicePreservesTwoFactorWriteBarrier(t *testing.T) {
	router, db, remote := exchangeFixtureWithLimit(t, true, exchangeAllowLimiter{}, 3)
	remote.snapshot = multiSnapshot(1)
	remote.snapshot.Keys[0].Group.Platform = "unknown"
	chosen := protocolBody(`"protocolSelections":[{"keyID":1,"protocol":"openai_chat_completions"}]`)
	w := exchangeRequest(router, chosen)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var user models.User
	db.First(&user)
	if err := db.Create(&models.UserTwoFactor{UserID: user.ID, TOTPEnabled: true, TOTPSecretEncrypted: "synthetic-encrypted-secret"}).Error; err != nil {
		t.Fatal(err)
	}
	var before models.Sub2APIIdentity
	var provider models.LLMUserProvider
	db.First(&before)
	db.First(&provider)
	remote.snapshot = multiSnapshot(2)
	remote.snapshot.Keys[0].Group.Platform = "unknown"
	remote.snapshot.Keys[1].Group.Platform = "unknown"
	remote.snapshot.Keys[0].Name = "不能提前重命名"
	remote.snapshot.User.Username = "不能提前写入"
	w = exchangeRequest(router, exchangeBody)
	if w.Code != 409 {
		t.Fatal(w.Body.String())
	}
	w = exchangeRequest(router, protocolBody(`"protocolSelections":[{"keyID":2,"protocol":"anthropic_messages"}]`))
	result := decodeExchange(t, w)
	if w.Code != 200 || !result.TwoFactorRequired || result.AccessToken != "" || result.User.ID != 0 || result.Sub2APIImport.Reused != 1 || result.Sub2APIImport.Items[1].Reason != "two_factor_required" {
		t.Fatalf("2FA=%d %s", w.Code, w.Body.String())
	}
	assertSub2APIExpiredCookie(t, w)
	var after models.Sub2APIIdentity
	var current models.LLMUserProvider
	db.First(&after)
	db.First(&current)
	if after.Username != before.Username || !after.ProfileSyncedAt.Equal(*before.ProfileSyncedAt) || current.Name != provider.Name || current.Protocol != provider.Protocol || current.APIKeyEnc != provider.APIKeyEnc {
		t.Fatal("2FA wrote profile/provider")
	}
	for _, model := range []any{&models.LLMUserProvider{}, &models.UserSession{}} {
		var count int64
		db.Model(model).Count(&count)
		if count != 1 {
			t.Fatalf("2FA wrote %T", model)
		}
	}
}

func TestSub2APIHTTPProtocolMemoryDoesNotCrossSubjectOrInstance(t *testing.T) {
	router, db, remote := exchangeFixture(t, true)
	remote.snapshot = multiSnapshot(1)
	remote.snapshot.Keys[0].Group.Platform = "unknown"
	chosen := protocolBody(`"protocolSelections":[{"keyID":1,"protocol":"openai_responses"}]`)
	if w := exchangeRequest(router, chosen); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	remote.snapshot.User.ID = 8
	remote.snapshot.Keys[0].UserID = 8
	w := exchangeRequest(router, strings.Replace(exchangeBody, `"userID":7`, `"userID":8`, 1))
	if w.Code != 409 {
		t.Fatal("cross-subject reuse", w.Code)
	}
	remote.snapshot.User.ID = 7
	remote.snapshot.Keys[0].UserID = 7
	// Move the fixture identity into another canonical instance. Same key ID/name
	// and local provider still exist, but the verified subject has no binding here.
	if err := db.Model(&models.Sub2APIIdentity{}).Where("remote_id = ?", 7).Updates(map[string]any{"instance": "https://other.example.com", "instance_hash": "other-instance-hash"}).Error; err != nil {
		t.Fatal(err)
	}
	w = exchangeRequest(router, exchangeBody)
	if w.Code != 409 {
		t.Fatal("cross-instance reuse", w.Code)
	}
	var count int64
	db.Model(&models.User{}).Count(&count)
	if count != 1 {
		t.Fatal("preflight created another account")
	}
}

func TestSub2APIHTTPProtocolAllSkippedDoesNotCreateAccount(t *testing.T) {
	router, db, remote := exchangeFixture(t, true)
	remote.snapshot = multiSnapshot(1)
	remote.snapshot.Keys[0].Group.Platform = "unknown"
	w := exchangeRequest(router, protocolBody(`"skipKeyIDs":[1]`))
	if w.Code != 403 || !strings.Contains(w.Body.String(), `"reason":"user_skipped"`) {
		t.Fatalf("skip=%d %s", w.Code, w.Body.String())
	}
	assertNoSub2APIProvisioning(t, db)
}
