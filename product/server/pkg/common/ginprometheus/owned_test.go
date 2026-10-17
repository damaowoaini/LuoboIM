package ginprometheus

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestOwnedAPIMetricsKeepRouteCountersAndSeparateRegistries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	first, firstHandler, err := NewOwnedPrometheus("app", nil)
	if err != nil {
		t.Fatal(err)
	}
	second, secondHandler, err := NewOwnedPrometheus("app", nil)
	if err != nil {
		t.Fatal(err)
	}
	build := func(metrics *Prometheus) *gin.Engine {
		r := gin.New()
		r.Use(metrics.HandlerFunc())
		r.GET("/owned", func(c *gin.Context) { c.Status(204) })
		return r
	}
	one, two := build(first), build(second)
	for i := 0; i < 2; i++ {
		out := httptest.NewRecorder()
		one.ServeHTTP(out, httptest.NewRequest("GET", "http://example.com/owned", nil))
		if out.Code != 204 {
			t.Fatal("metrics changed original handler")
		}
	}
	response := httptest.NewRecorder()
	firstHandler.ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
	firstBody := response.Body.String()
	if response.Code != 200 || !strings.Contains(firstBody, "app_requests_total{") || !strings.Contains(firstBody, `url="/owned"} 2`) || !strings.Contains(firstBody, "go_goroutines") || !strings.Contains(firstBody, "process_start_time_seconds") {
		t.Fatal("owned registry lost original metrics contract")
	}
	other := httptest.NewRecorder()
	secondHandler.ServeHTTP(other, httptest.NewRequest("GET", "/metrics", nil))
	if strings.Contains(other.Body.String(), `url="/owned"`) {
		t.Fatal("second owned instance borrowed first instance's counters")
	}
	out := httptest.NewRecorder()
	two.ServeHTTP(out, httptest.NewRequest("GET", "http://example.com/owned", nil))
	other = httptest.NewRecorder()
	secondHandler.ServeHTTP(other, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(other.Body.String(), `url="/owned"} 1`) {
		t.Fatal("second owned middleware not registered")
	}
}
