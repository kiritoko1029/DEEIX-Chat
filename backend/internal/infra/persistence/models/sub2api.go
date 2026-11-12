package models

import "time"

// Sub2APIIdentity has no soft-delete scope: deleting a local account must not
// silently transfer its remote identity to a newly registered account.
type Sub2APIIdentity struct {
	ControlPlaneModel
	InstanceHash    string `gorm:"size:64;not null;uniqueIndex:idx_identity_sub2api_subject,priority:1"`
	Instance        string `gorm:"size:2048;not null"`
	RemoteID        int64  `gorm:"not null;uniqueIndex:idx_identity_sub2api_subject,priority:2"`
	UserID          uint   `gorm:"not null;uniqueIndex:idx_identity_sub2api_user"`
	DisplayName     string `gorm:"size:128;not null;default:''"`
	Username        string `gorm:"size:128;not null;default:''"`
	Email           string `gorm:"size:254;not null;default:''"`
	ProfileSyncedAt *time.Time
}

func (Sub2APIIdentity) TableName() string { return "identity_sub2api_identities" }
