package controller

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/a923946240/xx-ui/v3/internal/web/service"
)

type ExternalOutboundController struct {
	exits service.ExternalOutboundService
}

func NewExternalOutboundController(g *gin.RouterGroup) *ExternalOutboundController {
	a := &ExternalOutboundController{}
	g.GET("/list", a.list)
	g.POST("/save", a.save)
	g.POST("/del/:id", a.del)
	g.POST("/probe/:id", a.probe)
	g.GET("/clients", a.clients)
	g.POST("/clients/check", a.checkClient)
	g.POST("/clients/install", a.installClient)
	return a
}

func (a *ExternalOutboundController) clients(c *gin.Context) {
	jsonObj(c, a.exits.Clients(), nil)
}

func (a *ExternalOutboundController) checkClient(c *gin.Context)   { a.clientOperation(c, false) }
func (a *ExternalOutboundController) installClient(c *gin.Context) { a.clientOperation(c, true) }

func (a *ExternalOutboundController) clientOperation(c *gin.Context, install bool) {
	var input service.ExternalClientRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2048)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		jsonObj(c, nil, errors.New("invalid external client request"))
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		jsonObj(c, nil, errors.New("invalid external client request"))
		return
	}
	jsonObj(c, nil, a.exits.StartClientOperation(input.Protocol, install))
}

func (a *ExternalOutboundController) list(c *gin.Context) {
	rows, err := a.exits.List()
	jsonObj(c, rows, err)
}

func (a *ExternalOutboundController) save(c *gin.Context) {
	var input service.ExternalOutboundInput
	if err := c.ShouldBindJSON(&input); err != nil {
		jsonObj(c, nil, errors.New("invalid external outbound request"))
		return
	}
	row, err := a.exits.Save(input)
	if err == nil {
		err = a.apply()
	}
	jsonObj(c, row, err)
}

func externalRequestID(c *gin.Context) (int, error) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id < 1 {
		return 0, errors.New("invalid external outbound id")
	}
	return id, nil
}

func (a *ExternalOutboundController) del(c *gin.Context) {
	id, err := externalRequestID(c)
	if err == nil {
		err = a.exits.Delete(id)
	}
	if err == nil {
		err = a.apply()
	}
	jsonObj(c, nil, err)
}

func (a *ExternalOutboundController) probe(c *gin.Context) {
	id, err := externalRequestID(c)
	if err != nil {
		jsonObj(c, nil, err)
		return
	}
	result, err := a.exits.Probe(c.Request.Context(), id)
	jsonObj(c, result, err)
}

func (a *ExternalOutboundController) apply() error {
	if err := a.exits.Reconcile(); err != nil {
		return errors.New("configuration saved but client activation failed")
	}
	x := &service.XrayService{}
	if x.IsXrayRunning() {
		if err := x.RestartXray(false); err != nil {
			return errors.New("configuration saved but Xray could not apply it")
		}
	}
	return nil
}
