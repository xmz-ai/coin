package id

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

type runtimeUUIDProvider struct{}

func NewRuntimeUUIDProvider() UUIDProvider {
	return runtimeUUIDProvider{}
}

func (runtimeUUIDProvider) NewUUIDv7() (string, error) {
	var raw [16]byte

	nowMs := uint64(time.Now().UTC().UnixMilli())
	raw[0] = byte(nowMs >> 40)
	raw[1] = byte(nowMs >> 32)
	raw[2] = byte(nowMs >> 24)
	raw[3] = byte(nowMs >> 16)
	raw[4] = byte(nowMs >> 8)
	raw[5] = byte(nowMs)

	if _, err := rand.Read(raw[6:]); err != nil {
		return "", err
	}

	raw[6] = (raw[6] & 0x0f) | 0x70 // version 7
	raw[8] = (raw[8] & 0x3f) | 0x80 // RFC4122 variant

	return formatUUID(raw), nil
}

func formatUUID(raw [16]byte) string {
	buf := make([]byte, 36)

	hex.Encode(buf[0:8], raw[0:4])
	buf[8] = '-'
	hex.Encode(buf[9:13], raw[4:6])
	buf[13] = '-'
	hex.Encode(buf[14:18], raw[6:8])
	buf[18] = '-'
	hex.Encode(buf[19:23], raw[8:10])
	buf[23] = '-'
	hex.Encode(buf[24:36], raw[10:16])
	return string(buf)
}
