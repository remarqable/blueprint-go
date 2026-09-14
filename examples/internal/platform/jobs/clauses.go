package jobs

import (
	"fmt"

	"gorm.io/gorm/clause"
)

type errNoHandler string

func (e errNoHandler) Error() string { return fmt.Sprintf("no handler for kind %q", string(e)) }

// skipLocked is PostgreSQL's SELECT ... FOR UPDATE SKIP LOCKED.
func skipLocked() clause.Locking {
	return clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}
}
