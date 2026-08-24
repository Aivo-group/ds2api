package enrollment

import "context"

// CamoufoxPlaceholder reserves the browser-adapter boundary without launching
// an anti-detect browser or attempting to bypass DeepSeek human verification.
// A future operator-controlled adapter may implement the same interface.
type CamoufoxPlaceholder struct{}

func (CamoufoxPlaceholder) RequestEmailCode(_ context.Context, _ Session) error {
	return &ManualActionRequiredError{
		URL:     DeepSeekSignupURL,
		Message: "open the official signup page and complete human verification before requesting the email code",
	}
}
