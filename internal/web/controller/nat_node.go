package controller

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"

	"github.com/gin-gonic/gin"

	"github.com/a923946240/xx-ui/v3/internal/web/runtime"
	"github.com/a923946240/xx-ui/v3/internal/web/service"
)

func natNodeChallenge(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	challenge, err := service.IssueNATNodeChallenge()
	if err != nil {
		jsonObj(c, nil, err)
		return
	}
	jsonObj(c, challenge, nil)
}

func natNodeObserve(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var request runtime.NATNodeObserveRequest
	if !decodeNATNodeBody(c, &request) {
		jsonObj(c, nil, errors.New("invalid NAT observation request"))
		return
	}
	jsonObj(c, nil, runtime.SendNATObservation(c.Request.Context(), request))
}

func natNodeResult(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var request runtime.NATNodeChallenge
	if !decodeNATNodeBody(c, &request) {
		jsonObj(c, nil, errors.New("invalid NAT observation request"))
		return
	}
	report, err := service.TakeNATNodeResult(request.Challenge)
	if err != nil {
		jsonObj(c, nil, err)
		return
	}
	jsonObj(c, report, nil)
}

func natNodeReport(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	// The value being synchronized is the directly connected TCP peer, so
	// neither reverse-proxy headers nor an address in the body can supply it.
	peer, ok := directNATPeer(c)
	var request runtime.NATNodeChallenge
	if !ok || !decodeNATNodeBody(c, &request) || !service.RecordNATNodeReport(request.Challenge, peer) {
		c.Status(http.StatusNotFound)
		return
	}
	c.Status(http.StatusNoContent)
}

func directNATPeer(c *gin.Context) (netip.Addr, bool) {
	peer, err := netip.ParseAddrPort(c.Request.RemoteAddr)
	if err != nil || c.Request.TLS == nil {
		return netip.Addr{}, false
	}
	for _, name := range [...]string{
		"Forwarded", "X-Forwarded-For", "X-Forwarded-Proto", "X-Forwarded-Host", "X-Forwarded-Port",
		"X-Real-IP", "Via", "CF-Connecting-IP", "True-Client-IP",
	} {
		if len(c.Request.Header.Values(name)) != 0 {
			return netip.Addr{}, false
		}
	}

	return peer.Addr(), true
}

func decodeNATNodeBody(c *gin.Context, payload any) bool {
	if c.ContentType() != "application/json" {
		return false
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(payload); err != nil {
		return false
	}
	var extra any
	return errors.Is(decoder.Decode(&extra), io.EOF)
}
