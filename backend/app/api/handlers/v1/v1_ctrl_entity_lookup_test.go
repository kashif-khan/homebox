package v1

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/sysadminsmedia/homebox/backend/internal/core/services"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/validate"
)

func TestLookupErrorStatuses(t *testing.T) {
	cases := map[error]int{
		services.ErrLookupDisabled:                                 http.StatusNotImplemented,
		services.ErrLookupNotReady:                                 http.StatusConflict,
		services.ErrLookupNotForLocations:                          http.StatusBadRequest,
		fmt.Errorf("%w: no such match", services.ErrLookupInvalid): http.StatusBadRequest,
	}
	for in, want := range cases {
		err := lookupError(in)
		var re *validate.RequestError
		if !errors.As(err, &re) || re.Status != want {
			t.Errorf("%v: got %v, want status %d", in, err, want)
		}
	}

	other := errors.New("boom")
	if got := lookupError(other); !errors.Is(got, other) {
		t.Errorf("unrelated errors must pass through unchanged, got %v", got)
	}
	if lookupError(nil) != nil {
		t.Error("nil must stay nil")
	}
}
