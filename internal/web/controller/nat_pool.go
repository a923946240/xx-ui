package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/a923946240/xx-ui/v3/internal/database/model"
	"github.com/a923946240/xx-ui/v3/internal/web/runtime"
	"github.com/a923946240/xx-ui/v3/internal/web/service"
)

const natPoolTokenKey = "nat_pool_matched_token"

func natPoolList(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	rows, err := service.ListNATPool()
	jsonObj(c, rows, err)
}

func natPoolReport(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	peer, ok := directNATPeer(c)
	if !ok || !runtime.IsPublicNATIPv4(peer.Unmap()) {
		c.Status(http.StatusNotFound)
		return
	}
	principal, name := natReportingPrincipal(c)
	if principal == "" {
		c.Status(http.StatusForbidden)
		return
	}
	var request runtime.NATPoolReport
	if !decodeNATNodeBody(c, &request) {
		jsonObj(c, nil, errors.New("invalid NAT address report"))
		return
	}
	entry, err := service.RecordNATPoolReport(principal, name, request, peer)
	jsonObj(c, entry, err)
}

func natReportingPrincipal(c *gin.Context) (string, string) {
	row, _ := c.Get(natPoolTokenKey)
	if token, ok := row.(*model.ApiToken); ok && token != nil {
		switch token.Scope {
		case model.ApiScopeAdmin, model.ApiScopeNodeSync, model.ApiScopeNodeAdmin:
			return "token:" + token.Name, token.Name
		}
	}
	if tls := c.Request.TLS; tls != nil && len(tls.VerifiedChains) > 0 && len(tls.VerifiedChains[0]) > 0 {
		sum := sha256.Sum256(tls.VerifiedChains[0][0].RawSubjectPublicKeyInfo)
		return "mtls:" + hex.EncodeToString(sum[:]), ""
	}
	return "", ""
}

func natReportingView(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	view, err := service.PanelNATReporting.View()
	jsonObj(c, view, err)
}

func natReportingSave(c *gin.Context) {
	var cfg service.NATReportingConfig
	if !decodeNATNodeBody(c, &cfg) {
		jsonObj(c, nil, errors.New("invalid NAT reporting settings"))
		return
	}
	view, err := service.PanelNATReporting.Save(c.Request.Context(), cfg)
	jsonObj(c, view, err)
}
