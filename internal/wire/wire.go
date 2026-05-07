package wire

import (
	"bytes"
	"encoding/binary"
	"net/http"
)

const (
	DomainRequest  = "commsrv-req-v1\x00"
	DomainRegister = "commsrv-register-v1\x00"
	DomainLogin    = "commsrv-login-v1\x00"
	DomainEnvelope = "commsrv-envelope-v2\x00"
)

func CanonicalRequest(method, fullURL string, ts int64, body []byte) []byte {
	var b bytes.Buffer
	writeString(&b, DomainRequest)
	writeString(&b, method)
	writeString(&b, fullURL)
	writeU64(&b, uint64(ts))
	writeBytes(&b, body)
	return b.Bytes()
}

func CanonicalRequestFromHTTP(r *http.Request, ts int64, body []byte) []byte {
	return CanonicalRequest(r.Method, r.URL.RequestURI(), ts, body)
}

func CanonicalRegister(inviteToken string, clientPub []byte, nameNFC string, ts int64) []byte {
	var b bytes.Buffer
	writeString(&b, DomainRegister)
	writeString(&b, inviteToken)
	writeBytes(&b, clientPub)
	writeString(&b, nameNFC)
	writeU64(&b, uint64(ts))
	return b.Bytes()
}

func CanonicalLogin(userID string, nonce []byte) []byte {
	var b bytes.Buffer
	writeString(&b, DomainLogin)
	writeString(&b, userID)
	writeBytes(&b, nonce)
	return b.Bytes()
}

func CanonicalEnvelope(id, to, from string, ts int64, nonce, ciphertext []byte) []byte {
	var b bytes.Buffer
	writeString(&b, DomainEnvelope)
	writeString(&b, id)
	writeString(&b, to)
	writeString(&b, from)
	writeU64(&b, uint64(ts))
	writeBytes(&b, nonce)
	writeBytes(&b, ciphertext)
	return b.Bytes()
}

func writeString(b *bytes.Buffer, value string) {
	writeBytes(b, []byte(value))
}

func writeBytes(b *bytes.Buffer, value []byte) {
	writeU32(b, uint32(len(value)))
	b.Write(value)
}

func writeU32(b *bytes.Buffer, value uint32) {
	var raw [4]byte
	binary.LittleEndian.PutUint32(raw[:], value)
	b.Write(raw[:])
}

func writeU64(b *bytes.Buffer, value uint64) {
	var raw [8]byte
	binary.LittleEndian.PutUint64(raw[:], value)
	b.Write(raw[:])
}
