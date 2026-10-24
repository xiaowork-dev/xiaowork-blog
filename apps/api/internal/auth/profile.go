package auth

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/platform/httpx"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/store"
)

const maxBioLen = 200 // user_profiles.bio varchar(200)

var ruleProfileUpdate = rateRule{Action: "PROFILE_UPDATE", Limit: 30, Window: time.Hour}

// PATCH /me/profile — {"nickname"?: string, "bio"?: string|null} (API-DD §4.2, §5.6). Omitted keys stay
// unchanged; bio null or "" clears it. Avatars use POST /me/avatar (P3).
func (s *Service) updateProfile(w http.ResponseWriter, r *http.Request) error {
	var in map[string]json.RawMessage
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	for k := range in {
		if k != "nickname" && k != "bio" {
			return httpx.ErrInvalidRequest
		}
	}
	ctx, u := r.Context(), User(r)
	fields := map[string][]string{}
	nickname, bio := u.Nickname, pgtype.Text{String: u.Bio, Valid: u.Bio != ""}

	if raw, ok := in["nickname"]; ok {
		var v string
		if json.Unmarshal(raw, &v) != nil {
			fields["nickname"] = []string{"INVALID"}
		} else if n, ok := validNickname(v); !ok {
			fields["nickname"] = []string{"INVALID_LENGTH"}
		} else {
			nickname = n
		}
	}
	if raw, ok := in["bio"]; ok {
		var v *string
		if json.Unmarshal(raw, &v) != nil {
			fields["bio"] = []string{"INVALID"}
		} else if b, problem := cleanBio(v); problem != "" {
			fields["bio"] = []string{problem}
		} else {
			bio = b
		}
	}
	if len(fields) > 0 {
		return httpx.FieldErrors(fields)
	}
	if err := s.limiter.hit(ctx, ruleProfileUpdate, u.ID.String()); err != nil {
		return err
	}
	err := store.ExecTx(ctx, s.pool, func(q *store.Queries) error {
		if err := q.UpdateUserNickname(ctx, store.UpdateUserNicknameParams{ID: u.ID, Nickname: nickname}); err != nil {
			return err
		}
		return q.UpsertProfileBio(ctx, store.UpsertProfileBioParams{UserID: u.ID, Bio: bio})
	})
	if err != nil {
		return err
	}
	httpx.WriteData(w, http.StatusOK, map[string]any{"nickname": nickname, "bio": bio.String})
	return nil
}

// cleanBio trims the text, allows line breaks and rejects other control/format characters.
func cleanBio(v *string) (pgtype.Text, string) {
	if v == nil {
		return pgtype.Text{}, ""
	}
	s := strings.TrimSpace(strings.ReplaceAll(*v, "\r\n", "\n"))
	if s == "" {
		return pgtype.Text{}, ""
	}
	if utf8.RuneCountInString(s) > maxBioLen {
		return pgtype.Text{}, "TOO_LONG"
	}
	for _, r := range s {
		if r != '\n' && (unicode.IsControl(r) || unicode.Is(unicode.Cf, r)) {
			return pgtype.Text{}, "INVALID_CHARACTERS"
		}
	}
	return pgtype.Text{String: s, Valid: true}, ""
}
