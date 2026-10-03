// Package ai — ИИ-агент платформы на Groq (OpenAI-совместимый API).
// Модель qwen/qwen3.8-27b с tool-calling: агент сам решает, какие тулзы
// платформы вызвать (команды на нодах, деплой, статус, файлы воркспейса).
package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	groqURL       = "https://api.groq.com/openai/v1/chat/completions"
	defaultModel  = "qwen/qwen3.8-27b"
	fallbackModel = "openai/gpt-oss-120b"
	maxRounds     = 24 // максимум циклов tool-call на один запрос
)

// Tool — описание тулзы для Groq (OpenAI-совместимый формат).
type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolFunction — схема функции.
type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Message — сообщение чата (роль: system/user/assistant/tool).
type Message struct {
	Role       string     `json:"role"`
	Content    any        `json:"content"` // string или nil при tool_calls
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// ToolCall — вызов тулзы моделью.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // JSON-строка
	} `json:"function"`
}

// chatRequest — тело запроса к Groq.
type chatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Tools       []Tool    `json:"tools,omitempty"`
	MaxTokens   int       `json:"max_completion_tokens,omitempty"`
	Temperature float64   `json:"temperature"`
	ToolChoice  string    `json:"tool_choice,omitempty"` // required/auto
}

// chatResponse — ответ Groq.
type chatResponse struct {
	Choices []struct {
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
	Usage struct {
		TotalTokens int `json:"total_tokens"`
	} `json:"usage"`
}

// Client — клиент Groq.
type Client struct {
	apiKey     string
	model      string
	httpClient *http.Client
}

// NewClient создаёт клиент Groq.
func NewClient(apiKey, model string) *Client {
	if model == "" {
		model = defaultModel
	}
	return &Client{
		apiKey:     apiKey,
		model:      model,
		httpClient: &http.Client{Timeout: 120 * time.Second},
	}
}

// chatOnce — запрос к API с ретраями на 429/5xx и фолбэком модели.
func (c *Client) chatOnce(req chatRequest) (*chatResponse, error) {
	models := []string{c.model}
	if c.model != fallbackModel {
		models = append(models, fallbackModel)
	}
	var lastErr error
	for _, m := range models {
		for attempt := 0; attempt < 3; attempt++ {
			if attempt > 0 {
				time.Sleep(time.Duration(2<<attempt) * time.Second) // 4с, 8с
			}
			req.Model = m
			cr, err := c.doOnce(req)
			if err == nil {
				return cr, nil
			}
			lastErr = err
			// ретраим только 429 и 5xx
			if !strings.Contains(err.Error(), "http 429") && !strings.Contains(err.Error(), "http 5") {
				break
			}
		}
	}
	return nil, lastErr
}

func (c *Client) doOnce(req chatRequest) (*chatResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequest(http.MethodPost, groqURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("groq http %d: %s", resp.StatusCode, truncate(string(data), 300))
	}
	var cr chatResponse
	if err := json.Unmarshal(data, &cr); err != nil {
		return nil, err
	}
	if cr.Error != nil {
		return nil, fmt.Errorf("groq: %s", cr.Error.Message)
	}
	if len(cr.Choices) == 0 {
		return nil, fmt.Errorf("groq: empty choices")
	}
	return &cr, nil
}

// RunChat — цикл агента: запрос -> tool-calls -> результаты -> ... -> финальный ответ.
// execTool вызывается для каждой тулзы; возвращает строку-результат для модели.
// onStep вызывается после каждого шага (для живого лога в UI).
//
// БАГ, который это чинит (v0.5.x → v0.6.0): на каждое сообщение юзера
// жёстко ставился tool_choice:"required". Модель обязана была вызвать тулзу.
// Но если задача не требовала действия («привет», «всё в порядке?»), Groq
// отвечал HTTP 400: "Tool choice is required, but model did not call a tool" —
// и агент падал на ровном месте.
//
// Теперь: сперва спрашиваем свободно (auto). Если модель ответила текстом,
// но по смыслу обещала действие («сейчас подключу ноду», «перезапущу»),
// делаем ОДИН добровольный запрос с required — и если он снова вернёт 400,
// спокойно отдаём пользователю текстовый ответ.
func (c *Client) RunChat(messages []Message, tools []Tool, execTool func(name, argsJSON string) (string, error), onStep func(step string)) (string, error) {
	isUserTurn := len(messages) > 0 && messages[len(messages)-1].Role == "user"
	nudged := false // уже пробовали «давай действие»

	for round := 0; round < maxRounds; round++ {
		req := chatRequest{Messages: messages, Tools: tools, Temperature: 0.3, MaxTokens: 8192}

		resp, err := c.chatOnce(req)
		if err != nil {
			// Страховка на случай, если модель всё же insists на required:
			// один раз пробуем без принуждения и не теряем диалог.
			if isUserTurn && !nudged && strings.Contains(err.Error(), "tool_use_failed") {
				nudged = true
				req.ToolChoice = "auto"
				resp, err = c.chatOnce(req)
			}
			if err != nil {
				return "", err
			}
		}
		msg := resp.Choices[0].Message

		// финальный ответ без тулзов
		if len(msg.ToolCalls) == 0 {
			text, _ := msg.Content.(string)
			// Модель пообещала действие, но не сделала — один раз настаиваем.
			if isUserTurn && !nudged && looksLikePromise(text) && len(tools) > 0 {
				nudged = true
				req.ToolChoice = "required"
				if forced, ferr := c.chatOnce(req); ferr == nil {
					fmsg := forced.Choices[0].Message
					if len(fmsg.ToolCalls) > 0 {
						msg = fmsg
					} else {
						return text, nil
					}
				} else {
					// модель упорно не зовёт тулзы — не ломаемся, отдаём текст
					return text, nil
				}
			} else {
				return text, nil
			}
		}

		// модель вызвала тулзы: добавляем её сообщение и выполняем
		messages = append(messages, msg)
		for _, tc := range msg.ToolCalls {
			if onStep != nil {
				onStep("вызов " + tc.Function.Name)
			}
			out, err := execTool(tc.Function.Name, tc.Function.Arguments)
			if err != nil {
				out = "ERROR: " + err.Error()
			}
			if onStep != nil {
				onStep(tc.Function.Name + " → " + truncate(out, 120))
			}
			messages = append(messages, Message{
				Role: "tool", Content: out, ToolCallID: tc.ID, Name: tc.Function.Name,
			})
		}
		isUserTurn = false
	}
	return "", fmt.Errorf("агент превысил лимит шагов (%d)", maxRounds)
}

// promiseMarkers — слова, по которым видно, что модель СОБИРАЛАСЬ что-то
// сделать, но только пообещала. Раньше это приводило к тупику: агент писал
// «сейчас подключу», ничего не делал и говорил «готово».
var promiseMarkers = []string{
	"сейчас подключ", "сейчас перезапущ", "сейчас обнов", "сейчас удал",
	"сейчас созд", "сейчас провед", "сейчас посмотр", "сейчас выполн",
	"сейчас сдела", "сейчас запущ", "сейчас настро", "давай подключ",
	"подключаю", "перезапускаю", "запускаю", "обновляю", "удаляю",
	"создаю", "проверяю", "выполняю", "запускаем", "подключаем",
	"перезапускаем", "проверяем", "разверну", "задеплою", "деплою",
}

// looksLikePromise — обещала ли модель действие, не сделав его.
func looksLikePromise(text string) bool {
	t := strings.ToLower(text)
	if t == "" {
		return false
	}
	for _, m := range promiseMarkers {
		if strings.Contains(t, m) {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
