package auth

import (
	"net/http"
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/models"
)

func TestSub2APIHTTPDeepSeekSingleKeyExchangeImportsResponses(t *testing.T) {
	router, db, remote := exchangeFixture(t, true)
	remote.snapshot = multiSnapshot(1)
	remote.snapshot.Keys[0].Group.Platform = "deepseek"
	remote.snapshot.APIBaseURL = "https://sub.example.com/v1/"

	response := exchangeRequest(router, exchangeBody)
	if response.Code != http.StatusOK {
		t.Fatalf("exchange=%d %s", response.Code, response.Body.String())
	}
	result := decodeExchange(t, response)
	summary := result.Sub2APIImport
	if summary == nil || summary.Total != 1 || summary.Eligible != 1 || summary.Imported != 1 || summary.Skipped != 0 || summary.Failed != 0 {
		t.Fatalf("summary=%+v", summary)
	}
	if result.AccessToken == "" || result.TwoFactorRequired || len(response.Result().Cookies()) != 1 {
		t.Fatal("exchange did not issue a session")
	}
	var provider models.LLMUserProvider
	if err := db.First(&provider).Error; err != nil {
		t.Fatal(err)
	}
	if provider.Protocol != "openai_responses" || provider.BaseURL != "https://sub.example.com/v1" || provider.Source != "sub2api" {
		t.Fatalf("protocol=%q baseURL=%q source=%q", provider.Protocol, provider.BaseURL, provider.Source)
	}
	if len(summary.Items) != 1 || summary.Items[0].ProviderID != provider.PublicID || summary.Items[0].Reason != "" {
		t.Fatalf("items=%+v", summary.Items)
	}

	repeated := exchangeRequest(router, exchangeBody)
	if repeated.Code != http.StatusOK {
		t.Fatalf("repeat=%d %s", repeated.Code, repeated.Body.String())
	}
	second := decodeExchange(t, repeated)
	if second.Sub2APIImport == nil || second.Sub2APIImport.Reused != 1 || second.User.ID != result.User.ID || second.Sub2APIImport.Items[0].ProviderID != provider.PublicID {
		t.Fatal("repeated DeepSeek exchange did not reuse identity and provider")
	}
	var count int64
	if err := db.Model(&models.LLMUserProvider{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("provider count=%d err=%v", count, err)
	}
}
