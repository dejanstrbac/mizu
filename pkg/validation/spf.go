package validation

import (
	"context"
	"net"

	"github.com/emersion/go-msgauth/authres"
	"github.com/mileusna/spf"
)

// CheckSPF performs an SPF check for the given IP, HELO host, and sender.
func CheckSPF(ctx context.Context, ip net.IP, heloHost, sender string) (*spf.Result, error) {
	// The spf library uses its own context handling internally, so we don't pass ctx.
	res := spf.CheckHost(ip, heloHost, sender, "")
	return &res, nil
}

// ConvertSPFResult converts a result from the mileusna/spf library to the
// standard authres.SPFResultValue used by the go-msgauth library.
func ConvertSPFResult(res spf.Result) authres.ResultValue {
	switch res {
	case spf.Pass:
		return authres.ResultPass
	case spf.Fail:
		return authres.ResultFail
	case spf.Softfail:
		return authres.ResultSoftFail
	case spf.Neutral:
		return authres.ResultNeutral
	default:
		return authres.ResultNone // Includes None, TempError, PermError
	}
}
