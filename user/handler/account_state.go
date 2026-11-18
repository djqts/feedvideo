package handler

import (
	"context"
	"errors"
)

// CurrentAccountStatus is the authenticated-route checker. Errors remain visible
// to the gate instead of being converted into a permissive boolean.
func (h *UserHomeHandler) CurrentAccountStatus(ctx context.Context, actor string) (int, error) {
	if h == nil || h.userService == nil {
		return 0, errors.New("account source unavailable")
	}
	return h.userService.CurrentAccountStatus(ctx, actor)
}
