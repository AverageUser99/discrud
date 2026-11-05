package discord

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Retain only safe diagnostic flags, never the response body, server-supplied
// messages, CAPTCHA challenge payloads, session tokens, or approval tickets.
func classifyAPIError(status int, body []byte) *APIError {
	e := &APIError{Status: status}
	if status == http.StatusTooManyRequests {
		e.RetryAfter = time.Second
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		e.UnexpectedResponse = true
		return e
	}
	if raw, ok := fields["code"]; ok && string(raw) != "null" {
		e.CodePresent = json.Unmarshal(raw, &e.Code) == nil
	}
	if status == http.StatusBadRequest {
		_, e.Captcha = fields["captcha_key"]
		if e.Captcha {
			var service string
			_ = json.Unmarshal(fields["captcha_service"], &service)
			if service == "hcaptcha" || service == "recaptcha" {
				e.CaptchaService = service
			}
		}
		var invalidFields map[string]json.RawMessage
		if json.Unmarshal(fields["errors"], &invalidFields) == nil {
			_, e.TicketRejected = invalidFields["ticket"]
		}
		var message string
		if json.Unmarshal(fields["message"], &message) == nil {
			switch strings.ToLower(strings.TrimSpace(message)) {
			case "invalid ticket", "invalid ticket.", "unknown ticket", "invalid remote auth ticket", "invalid remote auth ticket.":
				e.TicketRejected = true
			}
		}
	}
	if status == http.StatusTooManyRequests {
		var seconds float64
		if json.Unmarshal(fields["retry_after"], &seconds) == nil && seconds > 0 {
			if seconds > 600 {
				seconds = 600
			}
			e.RetryAfter = time.Duration(seconds * float64(time.Second))
			if e.RetryAfter < time.Second {
				e.RetryAfter = time.Second
			}
		}
	}
	return e
}
func qrExchangeFailure(err error) error {
	var api *APIError
	if errors.As(err, &api) && api.Captcha {
		return fmt.Errorf("Phone approval succeeded, but desktop sign-in needs another step: %w QR sign-in cannot display the CAPTCHA. You may choose the manual token sign-in option. No new desktop session was saved", err)
	}
	return fmt.Errorf("Phone approval succeeded, but desktop sign-in failed: %w No new desktop session was saved", err)
}

func (e *APIError) Error() string {
	detail := fmt.Sprintf("HTTP %d", e.Status)
	if e.CodePresent || e.Code != 0 {
		detail += fmt.Sprintf(", Discord code %d", e.Code)
	}
	if e.Status == http.StatusTooManyRequests {
		return fmt.Sprintf("Discord rate limit: retry in %.0f seconds. Nothing was retried automatically.", e.RetryAfter.Seconds()+1)
	}
	if e.Captcha {
		if e.CaptchaService != "" {
			detail += ", " + e.CaptchaService
		}
		return "Discord explicitly requested an interactive CAPTCHA (" + detail + ")."
	}
	if e.TicketRejected {
		return "Discord rejected the approval ticket (" + detail + "). Generate and scan a fresh QR code."
	}
	if e.UnexpectedResponse {
		return "Discord returned an unrecognized error response (" + detail + ")."
	}
	switch e.Status {
	case 401:
		return "Discord rejected the session credential (" + detail + "). Sign in again with QR or your own existing user token."
	case 403:
		return "Discord denied this action (" + detail + "): permissions, verification, or an unsupported endpoint."
	case 404:
		return "This channel or message is no longer available (" + detail + ")."
	case 400:
		if !e.CodePresent {
			detail += ", no Discord error code"
		}
		return "Discord rejected the request (" + detail + "). The response did not identify a CAPTCHA or rejected ticket."
	case 413:
		return "The attachment is too large (" + detail + ")."
	}
	return "Discord request failed (" + detail + ")."
}
