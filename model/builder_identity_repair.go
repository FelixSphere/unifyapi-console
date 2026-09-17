/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

import (
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// ErrBuilderLinkNotBroken is returned when an operator asks to archive a link
// whose account is fine. Archiving it would disconnect a working integration.
var ErrBuilderLinkNotBroken = errors.New("the Builder link is not broken; nothing to repair")

// ArchivedBuilderSubject is the subject an archived link is moved to. The id
// keeps it unique -- the same subject can be archived more than once over the
// life of a deployment -- and the whole thing is trimmed to the column's 128
// characters, which the prefix alone can push a full-length subject past.
func ArchivedBuilderSubject(id int, subject string) string {
	archived := fmt.Sprintf("%s%d:%s", BuilderArchivedSubjectPrefix, id, subject)
	if len(archived) > 128 {
		archived = archived[:128]
	}
	return archived
}

// ArchiveOrphanedBuilderIdentity frees a subject whose linked account no longer
// exists, so the Builder side can connect again.
//
// It archives rather than deletes. The row records which program and customer
// the account was enrolled in and when it claimed its grant, and that is the
// only account of a real enrollment; destroying it to fix a login problem
// would throw away billing history to save a reconnect. Moving the subject
// under the reserved prefix is enough, because every bridge path already
// refuses an archived subject -- the read side of this was built and only the
// write side was missing.
//
// Refuses a healthy link. An operator asking to repair one is either mistaken
// about the subject or about the problem, and archiving it would disconnect a
// working account.
func ArchiveOrphanedBuilderIdentity(subject string) (*BuilderIdentity, error) {
	if subject == "" {
		return nil, errors.New("subject is required")
	}
	if IsArchivedBuilderSubject(subject) {
		return nil, errors.New("that subject is already archived")
	}
	var archived BuilderIdentity
	err := DB.Transaction(func(tx *gorm.DB) error {
		var link BuilderIdentity
		if err := lockForUpdate(tx).Where("subject = ?", subject).First(&link).Error; err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&User{}).Where("id = ?", link.UserId).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return ErrBuilderLinkNotBroken
		}
		if err := tx.Model(&BuilderIdentity{}).Where("id = ?", link.Id).
			Update("subject", ArchivedBuilderSubject(link.Id, link.Subject)).Error; err != nil {
			return err
		}
		archived = link
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &archived, nil
}
