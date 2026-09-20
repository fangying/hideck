package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yibaiba/hideck/internal/config"
	"github.com/yibaiba/hideck/internal/notify"
)

type testBarkRequest struct {
	Enabled bool     `json:"enabled"`
	URLs    []string `json:"urls"`
	Group   string   `json:"group"`
	Icon    string   `json:"icon"`
	Level   string   `json:"level"`
}

type testBarkResponse struct {
	OK         bool     `json:"ok"`
	Message    string   `json:"message"`
	FailedURLs []string `json:"failed_urls,omitempty"`
}

func maskedBarkURLs(urls []string) []string {
	masked := make([]string, len(urls))
	for index := range masked {
		masked[index] = notificationSecretMask
	}
	return masked
}

func resolveBarkURLs(incoming, current []string) ([]string, error) {
	resolved := make([]string, 0, len(incoming))
	for index, rawURL := range incoming {
		value := strings.TrimSpace(rawURL)
		if value == "" {
			continue
		}
		if value != notificationSecretMask {
			resolved = append(resolved, value)
			continue
		}
		if index >= len(current) || strings.TrimSpace(current[index]) == "" {
			return nil, errors.New("Bark URL 脱敏值没有可保留的原配置")
		}
		resolved = append(resolved, current[index])
	}
	return resolved, nil
}

func (s *Server) handleTestBarkNotification(c *gin.Context) {
	var req testBarkRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "参数错误"})
		return
	}

	if !req.Enabled {
		c.JSON(http.StatusBadRequest, gin.H{"message": "请先启用 Bark 后再测试"})
		return
	}

	s.notificationConfigMu.Lock()
	current := s.fullCfg.Bark
	s.notificationConfigMu.Unlock()
	urls, err := resolveBarkURLs(req.URLs, current.URLs)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if len(urls) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"message": "至少需要一个有效的 Bark URL"})
		return
	}

	icon, err := resolveMaskedNotificationSecret(req.Icon, current.Icon, "Bark Icon URL")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	ch, err := notify.NewBarkChannel(config.BarkConfig{
		Enabled: true,
		URLs:    urls,
		Group:   strings.TrimSpace(req.Group),
		Icon:    icon,
		Level:   strings.TrimSpace(req.Level),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": "初始化 Bark 测试发送器失败: " + err.Error()})
		return
	}
	if ch == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": "Bark 测试发送器未初始化"})
		return
	}
	defer ch.Close()

	now := time.Now()
	ctx := notify.NotificationContext{
		Event:      "bark_test",
		Text:       "这是一条 Bark 测试通知",
		DeviceID:   "test_device_001",
		DeviceName: "测试设备",
		Timestamp:  now,
	}

	result, sendErr := ch.SendWithContextDetailed(ctx)
	if sendErr != nil {
		c.JSON(http.StatusOK, testBarkResponse{
			OK:         false,
			Message:    "测试通知发送失败: " + sendErr.Error(),
			FailedURLs: maskedBarkURLs(result.FailedURLs),
		})
		return
	}

	c.JSON(http.StatusOK, testBarkResponse{
		OK:      true,
		Message: "测试通知已发送",
	})
}
