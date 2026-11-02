package controller

import (
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
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
	return a
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
