package messagedelivery

import "errors"

// Metadata-only visible stop. Unknown/unattributable bodies stay on the
// original broker: this error does not create an unredactable raw store.
// Never include the original error text, actor, headers, body or credentials.
type UnsupportedCustodyBlockedError struct {
	Topic      string
	Partition  int32
	Offset     int64
	BodySHA256 string
	Reason     string
	cause      error
}

func (e *UnsupportedCustodyBlockedError) Error() string {
	return "unsupported modifier custody blocked: " + e.Reason
}
func (e *UnsupportedCustodyBlockedError) Unwrap() error { return e.cause }
func BlockUnsupportedCustody(record KafkaRecord, cause error) error {
	reason := "custody-unavailable"
	switch {
	case errors.Is(cause, ErrUnsupportedCustodyScope):
		reason = "scope-not-approved"
	case errors.Is(cause, ErrUnsupportedCustodyExpired):
		reason = "deadline-expired"
	case errors.Is(cause, ErrCommitUnconfirmed):
		reason = "custody-unconfirmed"
	case errors.Is(cause, ErrConflict):
		reason = "source-mismatch"
	case errors.Is(cause, ErrInvalid):
		reason = "source-not-attributable"
	}
	return &UnsupportedCustodyBlockedError{Topic: record.Topic, Partition: record.Partition, Offset: record.Offset, BodySHA256: hash(record.Body), Reason: reason, cause: cause}
}
