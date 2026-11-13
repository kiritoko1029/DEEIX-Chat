package user

import (
	"context"
	"testing"

	domainuser "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/user"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
)

type defaultTimezoneRepo struct {
	repository.UserRepository
	created *domainuser.User
}

func (*defaultTimezoneRepo) GetByUsername(context.Context, string) (*domainuser.User, error) {
	return nil, repository.ErrNotFound
}

func (r *defaultTimezoneRepo) CreateWithCredential(_ context.Context, input repository.CreateWithCredentialInput) error {
	r.created = input.User
	return nil
}

func TestCreateUserDefaultTimezoneAndExplicitPreference(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"", "Asia/Shanghai"},
		{"  ", "Asia/Shanghai"},
		{"Etc/UTC", "Etc/UTC"},
		{"Europe/Berlin", "Europe/Berlin"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			repo := &defaultTimezoneRepo{}
			created, err := NewService(repo).CreateUser(context.Background(), CreateUserInput{
				Username: "timezone-user", Password: "synthetic-fixture-pass", Timezone: tc.input,
			})
			if err != nil || created == nil || repo.created == nil {
				t.Fatal("user creation failed", err)
			}
			if created.Timezone != tc.want || repo.created.Timezone != tc.want {
				t.Fatalf("timezone=%q want=%q", created.Timezone, tc.want)
			}
		})
	}
}
