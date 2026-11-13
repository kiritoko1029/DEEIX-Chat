package auth

import (
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/models"
)

func TestSub2APICreationDefaultsToShanghaiWithoutChangingExistingTimezone(t *testing.T) {
	router, db, _ := exchangeFixture(t, true)
	first := exchangeRequest(router, exchangeBody)
	if first.Code != 200 {
		t.Fatal(first.Code, first.Body.String())
	}
	user := decodeExchange(t, first).User
	if user.Timezone != "Asia/Shanghai" {
		t.Fatalf("new timezone=%q", user.Timezone)
	}
	if err := db.Model(&models.User{}).Where("id = ?", user.ID).Update("timezone", "Europe/Berlin").Error; err != nil {
		t.Fatal(err)
	}
	second := exchangeRequest(router, exchangeBody)
	if second.Code != 200 {
		t.Fatal(second.Code, second.Body.String())
	}
	reused := decodeExchange(t, second).User
	if reused.ID != user.ID || reused.Timezone != "Europe/Berlin" {
		t.Fatalf("existing preference changed: %+v", reused)
	}
}
