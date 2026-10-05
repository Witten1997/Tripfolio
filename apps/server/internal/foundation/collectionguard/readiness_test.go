package collectionguard

import "testing"

func TestReadinessRequiresEveryPrerequisite(t *testing.T) {
	for mask := 0; mask < 16; mask++ {
		r := Readiness{mask&1 != 0, mask&2 != 0, mask&4 != 0, mask&8 != 0}
		err := r.Check()
		if mask == 15 {
			if err != nil {
				t.Fatal(err)
			}
		} else {
			assertCode(t, err, 409, "SYNC_NOT_READY")
		}
	}
}
