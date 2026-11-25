package settings

import (
	"errors"
	"math"
	"strconv"
	"strings"
)

func generationParameters(temperature, output string) (*float64, *int, error) {
	var sampling *float64
	var tokens *int
	if text := strings.TrimSpace(temperature); text != "" {
		value, err := strconv.ParseFloat(text, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 2 {
			return nil, nil, errors.New("采样温度须为 0 至 2 的数字")
		}
		sampling = &value
	}
	if text := strings.TrimSpace(output); text != "" {
		value, err := strconv.Atoi(text)
		if err != nil || value < 1 || value > 131072 {
			return nil, nil, errors.New("最大输出 Token 须为 1 至 131072 的整数")
		}
		tokens = &value
	}
	return sampling, tokens, nil
}

func (s *IntegrationState) generationParameterError() error {
	if s.DraftMode != "provider" {
		return nil
	}
	_, _, err := generationParameters(s.Temperature, s.MaxOutputTokens)
	return err
}
