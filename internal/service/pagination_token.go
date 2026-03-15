package service

import (
	"strconv"
	"strings"
	"time"
)

func EncodePageToken(t time.Time, txnNo string) string {
	return strconv.FormatInt(t.UnixNano(), 10) + "|" + txnNo
}

func DecodePageToken(token string) (time.Time, string, bool) {
	parts := strings.SplitN(token, "|", 2)
	if len(parts) != 2 {
		return time.Time{}, "", false
	}
	ns, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return time.Time{}, "", false
	}
	return time.Unix(0, ns).UTC(), parts[1], true
}
