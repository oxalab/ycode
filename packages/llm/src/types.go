package llm

import (
	"context"
	"net/http"
)

type Api string

const (
	OpenAICompletions     Api = "openai-completions"
	MistalConversations   Api = "mistral-converations"
	OpenAIResponses       Api = "openai-responses"
	AzureOpenAIResponses  Api = "azure-openai-responses"
	OpenAICodexResponses  Api = "openai-codex-responses"
	AnthropicMessages     Api = "anthropic-messages"
	BedrockConverseStream Api = "beedrock-converse-stream"
	GoogleGenerativeAI    Api = "google-generative-ai"
	GoogleVertexAI        Api = "google-vertex"
)

type Provider string

const (
	AmazonBedrock      Provider = "amazon-bedrock"
	Anthropic          Provider = "anthropic"
	Google             Provider = "google"
	GoogleVertex       Provider = "google-vertex"
	OpenAI             Provider = "openai"
	OpenAICodex        Provider = "openai-codex"
	Deepseek           Provider = "deepseek"
	GithubCopilot      Provider = "github-copilot"
	XAI                Provider = "xai"
	Groq               Provider = "groq"
	Cerebras           Provider = "cerebras"
	Openrouter         Provider = "openrouter"
	VercelAIGatewway   Provider = "vercel-ai-gateway"
	ZAI                Provider = "zai"
	Mistral            Provider = "mistral"
	Minimax            Provider = "minimax"
	MinimaxCN          Provider = "minimax-cn"
	Huggingface        Provider = "huggingface"
	Fireworks          Provider = "fireworks"
	Opencode           Provider = "opencode"
	OpencodeGo         Provider = "opencode-go"
	KimiCoding         Provider = "kimi-coding"
	CloudflareWorkerAI Provider = "cloudflare-workers-ai" // TS spelling; gateway handled by string match in cloudflare.go
)

type ThinkingLevel string

const (
	Minimal ThinkingLevel = "minimal"
	Low     ThinkingLevel = "low"
	Medium  ThinkingLevel = "medium"
	High    ThinkingLevel = "high"
	XHigh   ThinkingLevel = "xhigh"
)

/* Token Budgets for each thinking level (strictly for token based providers)*/
type ThinkingBudgets struct {
	Minimal *int `json:"minimal,omitempty"`
	Low     *int `json:"low,omitempty"`
	Medium  *int `json:"medium,omitempty"`
	High    *int `json:"high,omitempty"`
}

/*Base Options all providers share*/
type CacheRetention string

const (
	CacheRetentionNone  CacheRetention = "none"
	CacheRetentionShort CacheRetention = "short"
	CacheRetentionLong  CacheRetention = "long"
)

type Transport string

const (
	TransportSSE       Transport = "sse"
	TransportWebsocket Transport = "websocket"
	TransportAuto      Transport = "auto"
)

type StopReason string 

const (
	StopReasonStop 		StopReason = "stop"
	StopReasonLength	StopReason = "length"
	StopReasonToolUse	StopReason = "tooluse"
	StopReasonError 	StopReason = "error"
	StopReasonAborted	StopReason = "aborted"
)

type Role string 

const (
	RoleUser 		Role = "user"
	RoleAssistant	Role = "assistant"
	RoleTool 		Role = "tool"
)

type StreamOptions struct {
	Temperature 	 *float64 			`json:"temperature,omitempty"`
	MaxTokens   	 *int     			`json:"maxTokens,omitempty"`
	Context     	 context.Context 	`json:"-"`
	APIKey			 string 			`json:"apiKey,omitempty"`
	/*
	Preferred transport for porviders that support multiple transports
	Providers that do not support this option, ignore it.
	*/
	Transport 		 Transport 			`json:"transport,omitempty"`
	/*
	Prompt Cache Retention preference. Providers map this to their supported values.
	Default: "short"
	*/
	CacheRetention 	 CacheRetention 	`json:"cacheRetention,omitempty"`
	/*
	Session Identifier for providers that support session-based caching.
	Providers can use this to enable prompt caching, request routing, or other.
	session-aware features. Ignored by providers that don't support it
	*/
	SessionID		 string 			`json:"sessionId,omitempty"`
	/*
	custom HTTP Headers to include in API Requests
	Merged with provider defaults; can override default headers.
	Not supported by all providers (e.g. AWS Bedrock uses SDK auth)
	*/
	Headers 		 map[string]string 	`json:"headers,omitempty"`
	/*
	HTTP request timeout in milliseconds for providers/SDKs that support it.
	For example, OpenAI and Anthropic SDK clients default to 10 minutes.
	*/
	TimeoutMs		 int 				`json:"timeoutMs,omitempty"`
	/*
	Maximum retry attempts for providers/SDKs that support client-side retries.
	For example, OpenAI and Anthropic SDK clients default to 2.
	*/
	MaxRetries		 *int 				`json:"maxRetries,omitempty"`
	/*
	* Maximum delay in milliseconds to wait for a retry when the server requests a long wait.
	If the server's requested delay exceeds this value, the request fails immediately
	with an error containing the requested delay, allowing higher-level retry logic
	to handle it with user visibility.
	Default: 60000 (60 seconds). Set to 0 to disable the cap.
	*/
	MaxRetryDelayMs int 				`json:"maxRetryDelayMs,omitempty"`
	/**
	 * Optional metadata to include in API requests.
	 * Providers extract the fields they understand and ignore the rest.
	 * For example, Anthropic uses `user_id` for abuse tracking and rate limiting.
	 */
	Metadata		map[string]any 		`json:"metadata,omitempty"`

	// Callbacks
	OnPayload		func(payload any) any `json:"-"`
	OnResponse  	func(resp *http.Response) `json:"-"`
}

type ProviderStreamOptions struct {
	StreamOptions
	Extra 	map[string]any  `json:",inline"`
}

// SimpleStreamOptions extends StreamOptions by adding reasonin/thinking fields.
type SimpleStreamOptions struct {
	// Embedding StreamOptions makes all its fields available directly on SimpleStreamOptions
	StreamOptions
	// Reasoning maps to ThinkingLevel type
	Reasoning ThinkingLevel  `json:"reasoning,omitempty"`
	// ThinkingBudget
	ThinkingBudgets *ThinkingBudgets `json:"thinkingBudgets,omitempty"`
}

/*
----Content Blocks (The Union)----
*/

// ContentBlock is a marker interface for content types.
type ContentBlock interface {
	IsContentBlock()
}

type TextContent struct {
	Type 			string		`json:"type"`
	Text 			string 		`json:"text"`
	TextSignature	string 		`json:"textSignature,omitempty"`
}

func (TextContent) IsContentBlock() {}

type ThinkingContent struct {
	Type 				string 		`json:"type"`
	Thinking 			string 		`json:"thinking"`
	ThinkingSignature	string 		`json:"thinkingSignature,omitempty"`
	Redacted 			*bool 		`json:"redacted,omitempty"`
}

func (ThinkingContent) IsContentBlock() {}

type ImageContent struct{
	Type 		string 		`json:"type"`
	Data 		string 		`json:"data"`
	MimeType 	string 		`json:"mimeType"`
}

func (ImageContent) IsContentBlock() {}

type ToolCall struct {
	Type 		string 			`json:"type"`
	ID 			string  		`json:"id"`
	Name 		string 			`json:"name"`
	Arguments 	map[string]any 	`json:"arguments"`
	ThoughtSignature 	string 	`json:"thoughSignature,omitempty"` // Google Specific: Opaque signature for reusing thought context
	// Streaming scratch: accumulated raw argument string while the call is
	// still in flight. Never serialized (the reference deletes it at finalize).
	PartialArgs string `json:"-"`
}

func (ToolCall) IsContentBlock() {}

// --- Messages & Chat Context ---

// Message is a marker interface for the different message types.
type Message interface {
	IsMessage()
}

type UsageCost struct {
	Input 		float64 	`json:"input"`
	Output 		float64 	`json:"output"`
	CacheRead	float64 	`json:"cacheRead"`
	CacheWrite 	float64  	`json:"cacheWrite"`
	Total 		float64 	`json:"total"`
}

type Usage struct {
	Input  		int 		`json:"input"`
	Output  	int 		`json:"output"`
	CacheRead 	int 		`json:"cacheRead"`
	CacheWrite 	int 		`json:"cacheWrite"`
	TotalTokens int 		`json:"totalTokens"`
	Cost 		UsageCost 	`json:"cost"`
}

type UserMessage struct {
	Role 	  Role 				`json:"role"`
	Content   []ContentBlock 	`json:"content"`
	Timestamp int64 			`json:"timestamp"`
}

func (UserMessage) IsMessage() {}

type AssistantMessage struct {
	Role 		Role 			`json:"role"`
	Content 	[]ContentBlock 	`json:"content"`
	Api 		Api 			`json:"api"`
	Provider 	Provider 		`json:"provider"`
	Model 		string 			`json:"model"`
	ResponseID 	string 			`json:"responseId,omitempty"`
	// Model actually served the request, when the stream reports one and it
	// differs from the requested id.
	ResponseModel string 		`json:"responseModel,omitempty"`
	Usage 		Usage 			`json:"usage"`
	StopReason 	StopReason 		`json:"stopReason"`
	ErrorMessage string 		`json:"errorMessage,string"`
	Timestamp 	int64 			`json:"timestamp"`
}

func (AssistantMessage) IsMessage() {}

type ToolResultMessage[TDetails any] struct {
	Role 		Role 			`json:"role"`
	ToolCallID 	string 			`json:"toolCallId"`
	ToolName 	string 			`json:"toolName"`
	Content 	[]ContentBlock 	`json:"content"`
	Details 	*TDetails 		`json:"details,omitempty"`
	IsError 	bool 			`json:"isError"`
	Timestamp 	int64 			`json:"timestamp"`
}

func(ToolResultMessage[TDetails]) IsMessage() {}

type Tool[TParameters any] struct {
	Name 		string 		`json:"name"`
	Description string 		`json:"description"`
	Parameters 	TParameters `json:"parameters"`
}

type ChatContext struct {
	SystemPrompt string 		`json:"systemPrompt,omitempty"`
	Messages 	 []Message 		`json:"messages"`
	Tools 		 []Tool[any]  	`json:"tools,omitempty"`
}

// --- Compatibility & Routing ---

type OpenAICompletionsCompat struct {
	/*Whether provider supports the `store` field. Auto-detected from URL*/
	SupportStore 								*bool 						`json:"supportsStore,omitempty"`
	/* Whether the provider supports the `developer` role (vs `system`). Auto-detected from URL (default) */
	SupportsDeveloperRole						*bool 						`json:"supportsDeveloperRole,omitempty"`
	/* Whether the providers supports `reasoning_effort`. Default: auto-detected from URL */
	SupportsReasoningEffort 					*bool 						`json:"supportsReasoningEffort,omitempty"`
	/* Optional Mapping from y-ai reasoning level to provider/model-specific `reasoning_effort` values. */
	ReasoningEffortMap 							map[ThinkingLevel]string 	`json:"reasoningEffortMap,omitempty"`
	/* Whether the provider supports `stream_options: { include_usage: true }` for token usage in streaming responses. Default: true. */
	SupportUsageInStreaming						*bool 						`json:"supportsUsageInStreaming,omitempty"`
	/* Which field to use for max tokens. Default: auto-detected from URL. */
	MaxTokensField 								*string 					`json:"maxTokensField,omitempty"`
	/* Whether tool results require the `name` field. Default: auto-detected from URL. */
	RequireToolResultName						*bool						`json:"requiresToolResultName,omitempty"`
	/* Whether a user message after tool results requires an assistant message in between. Default: auto-detected from URL. */
	RequireAssistantAfterToolResult 			*bool 						`json:"requiresAssistantAfterToolResult,omitempty"`
	/* Whether thinking blocks must be converted to text blocks with <thinking> delimiters. Default: auto-detected from URL. */
	RequireThinkingAsText 						*bool 						`json:"requiresThinkingAsText,omitempty"`
	/* Whether all replayed assistant messages must include an empty reasoning_content field when reasoning is enabled. Default: auto-detected from URL. */
	RequireReasoningContentOnAssistantMessages	*bool 						`json:"requiresReasoningContentOnAssistantMessages,omitempty"`
	/* Format for reasoning/thinking parameter. "openai" uses reasoning_effort, "openrouter" uses reasoning: { effort }, "deepseek" uses thinking: { type } plus reasoning_effort, "zai" uses top-level enable_thinking: boolean, "qwen" uses top-level enable_thinking: boolean, and "qwen-chat-template" uses chat_template_kwargs.enable_thinking. Default: "openai". */
	ThinkingFormat 								*string 					`json:"thinkingFormat,omitempty"`
	/* OpenRouter-specific routing preferences. Only used when baseUrl points to OpenRouter. */
	OpenRouterRouting 							*OpenRouterRouting 			`json:"openRouterRouting,omitempty"`
	/* Vercel AI Gateway routing preferences. Only used when baseUrl points to Vercel AI Gateway. */
	VercelGatewayRouting 						*VercelGatewayRouting 		`json:"vercelGatewayRouting,omitempty"`
	/* Whether z.ai supports top-level `tool_stream: true` for streaming tool call deltas. Default: false. */
	ZaiToolStream 								*bool 						`json:"zaiToolStream,omitempty"`
	/* Whether the provider supports the `strict` field in tool definitions. Default: true. */
	SupportsStrictMode 							*bool 						`json:"supportsStrictMode,omitempty"`
	/* Cache control convention for prompt caching. "anthropic" applies Anthropic-style `cache_control` markers to the system prompt, last tool definition, and last user/assistant text content. */
	CacheControlFormat 							*string 					`json:"cacheControlFormat,omitempty"`
	/* Whether to send known session-affinity headers (`session_id`, `x-client-request-id`, `x-session-affinity`) from `options.sessionId` when caching is enabled. Default: false. */
	SendSessionAffinityHeaders 					*bool 						`json:"sendSessionAffinityHeaders,omitempty"`
	/* Whether the provider supports long prompt cache retention (`prompt_cache_retention: "24h"` or Anthropic-style `cache_control.ttl: "1h"`, depending on format). Default: true. */
	SupportsLongCacheRetention 					*bool 						`json:"supportsLongCacheRetention"`
}

type OpenAIResponsesCompat struct {
	SendSessionIDHeader 			*bool 			`json:"sendSessionIdHeader,omitempty"`
	SupportsLongCacheRetention 		*bool 			`json:"supportsLongCacheRetention,omitempty"`
}

type AnthropicMessagesCompat struct {
	/**
	 * Whether the provider accepts per-tool `eager_input_streaming`.
	 * When false, the Anthropic provider omits `tools[].eager_input_streaming`
	 * and sends the legacy `fine-grained-tool-streaming-2025-05-14` beta header
	 * for tool-enabled requests.
	 * Default: true.
	*/
	SupportsEagerToolInputStreaming 	*bool 	`json:"supportsEagerToolInputStreaming,omitempty"`
	SupportsLongCacheRetention 			*bool 	`json:"supportsLongCacheRetention,omitempty"`
	// Send the x-session-affinity header from SessionID when caching is
	// enabled (Fireworks / cloudflare-ai-gateway-anthropic). Default: auto.
	SendSessionAffinityHeaders *bool `json:"sendSessionAffinityHeaders,omitempty"`
	// Whether cache_control may be attached to tool definitions. Default: true.
	SupportsCacheControlOnTools *bool `json:"supportsCacheControlOnTools,omitempty"`
}


/**
 * OpenRouter provider routing preferences.
 * Controls which upstream providers OpenRouter routes requests to.
 * Sent as the `provider` field in the OpenRouter API request body.
 * @see https://openrouter.ai/docs/guides/routing/provider-selection
*/
type OpenRouterRouting struct {
	AllowFallbacks 			*bool 					`json:"allow_fallback,omitempty"`
	RequireParameters 		*bool 					`json:"require_parameters,omitempty"`
	DataCollection 			*string 				`json:"data_collection,omitempty"`
	Zdr 					*bool 					`json:"zdr,omitempty"`
	EnforceDistillableText 	*bool 					`json:"enforce_distillable_text,omitempty"`
	Order 					[]string 				`json:"order,omitempty"`
	Only 					[]string 				`json:"only,omitempty"`
	Ignore 					[]string  				`json:"ignore,omitempty"`
	Quantizations 			[]string 				`json:"quantizations,omitempty"`
	Sort 					*OpenRouterSort			`json:"sort,omitempty"`
	MaxPrice 				*OpenRouterMaxPrice 	`json:"max_price,omitempty"`
	PreferredMinThroughput 	*OpenRouterThreshold  	`json:"preferred_min_throughput,omitempty"`
	PreferredMaxLatency 	*OpenRouterThreshold 	`json:"preferred_max_latency,omitempty"`
}

type OpenRouterSort struct {
	By 		  *string  	`json:"by,omitempty"` // "price", "throughput" or "latency"
	Partition *string 	`json:"partition,omitempty"` // "model" or "none"		
}

type OpenRouterMaxPrice struct {
	Prompt 			*float64 		`json:"prompt,omitempty"`
	Completion 		*float64 		`json:"completion,omitempty"`
	Image 			*float64 		`json:"image,omitempty"`
	Audio 			*float64 		`json:"audio,omitempty"`
	Request 		*float64 		`json:"request,omitempty"`
}

type OpenRouterThreshold struct {
	/* Maximum latency in seconds at the 50th percentile. */
	P50 *float64 `json:"p50,omitempty"`
	/* Maximum latency in seconds at the 75th percentile. */
	P75 *float64 `json:"p75,omitempty"`
	/* Maximum latency in seconds at the 90th percentile. */
	P90 *float64 `json:"p90,omitempty"`
	/* Maximum latency in seconds at the 99th percentile. */
	P99 *float64 `json:"p99,omitempty"`	
}

/**
 * Vercel AI Gateway routing preferences.
 * Controls which upstream providers the gateway routes requests to.
 * @see https://vercel.com/docs/ai-gateway/models-and-providers/provider-options
*/
type VercelGatewayRouting struct {
	/* List of provider slugs to exclusively use for this request (e.g., ["bedrock", "anthropic"]). */
	Only 		[]string 		`json:"only,omitempty"`
	/* List of provider slugs to try in order (e.g., ["anthropic", "openai"]). */
	Order 		[]string 		`json:"order,omitempty"`
}

type ModelCost struct {
	Input 		float64  `json:"input"`
	Output 		float64  `json:"output"`
	CacheRead 	float64  `json:"cacheRead"`
	CacheWrite 	float64  `json:"cacheWrite"`
	Total 		float64  `json:"total"`
}

type InputType string

const (
	InputText  InputType = "text"
	InputImage InputType = "image"
	InputAudio InputType = "audio"
	InputVideo InputType = "video"
)

type Compat struct {
	OpenAICompletions *OpenAICompletionsCompat `json:"openai_completions,omitempty"`
	OpenAIResponses   *OpenAIResponsesCompat   `json:"openai_responses,omitempty"`
	AnthropicMessages *AnthropicMessagesCompat `json:"anthropic_messages,omitempty"`
}

type Model[TApi Api] struct {
	ID 	 			string 				`json:"id"`
	Name 			string 				`json:"name"`
	Api 			TApi 				`json:"api"`
	Provider 		Provider 			`json:"provider"`
	BaseURL 		string 				`json:"baseUrl"`
	Reasoning 		bool 				`json:"reasoning"`
	Input 			[]InputType 		`json:"input"`  // "text" | "image"
	Cost 			ModelCost 			`json:"cost"`
	ContextWindow	int 				`json:"contextWindow"`
	MaxTokens 		int 				`json:"maxTokens"`
	/*
	Reasoning level mapping (types.ts thinkingLevelMap). Keys are level names;
	a nil value is the TS null ("level unavailable"), an absent key is
	undefined. Drives xhigh availability and effort remapping.
	*/
	ThinkingLevelMap 	map[string]*ThinkingLevel 	`json:"thinkingLevelMap,omitempty"`
	Headers 		map[string]string 	`json:"headers,omitempty"`
	/** Compatibility overrides for OpenAI-compatible APIs. If not set, auto-detected from baseUrl. */
	Compat 			Compat				`json:"compat,omitempty"`
}

// --- Event Streams & Functions ---

type AssistantMessageEvent struct {
	Type         string            `json:"type"` // "start", "text_start", "done", etc.
	Partial      *AssistantMessage `json:"partial,omitempty"`
	ContentIndex *int              `json:"contentIndex,omitempty"`
	Delta        string            `json:"delta,omitempty"`
	Content      string            `json:"content,omitempty"`
	ToolCall     *ToolCall         `json:"toolCall,omitempty"`
	Reason       StopReason        `json:"reason,omitempty"`
	Message      *AssistantMessage `json:"message,omitempty"` // populated on "done"
	Error        *AssistantMessage `json:"error,omitempty"`   // populated on "error"
}


// StreamFunction definition
// TOptions is unconstrained because callers may pass different concrete option structs.
// Using a narrow struct constraint would make extension types harder to use.
type StreamFunction[TApi Api, TOptions any] func(
	ctx context.Context,
	model Model[TApi],
	chatCtx ChatContext,
	options *TOptions,
) <-chan AssistantMessageEvent 