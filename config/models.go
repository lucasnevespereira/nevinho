package config

// KnownModels is the canonical list of friendly model names per provider
// that nevinho recognizes. Names outside this list resolve only when the
// provider is Ollama (any local model name allowed) so a typo or stale
// saved name fails loudly instead of producing 400s at request time.
//
// Dated variants like claude-haiku-4-5-20251001 are accepted by the upstream
// API and used internally as defaults, but are not listed here to keep the
// user facing menu compact.
//
// It lives in config, not llm, so the setup wizard and the llm resolver
// share it without an import cycle. The setup wizard treats the first
// entry of each list as that provider's default.
var KnownModels = map[string][]string{
	"anthropic": {
		"claude-haiku-4-5",
		"claude-sonnet-4-6",
		"claude-opus-4-6",
		"claude-opus-4-7",
	},
	"openai": {
		"gpt-4o-mini",
		"gpt-5-nano",
		"gpt-5-mini",
		"gpt-5",
		"gpt-4o",
		"gpt-4-turbo",
		"o1-mini",
		"o3-mini",
		"o4-mini",
	},
	"gemini": {
		"gemini-2.5-flash",
		"gemini-2.5-pro",
		"gemini-3.1-flash-lite",
	},
	// Groq's free tier covers all listed models within rate limits
	// (~14k req/day at writing). Names are passed through to Groq's
	// OpenAI compatible endpoint with the "groq:" prefix stripped.
	"groq": {
		"groq:llama-3.3-70b-versatile",
		"groq:llama-3.1-8b-instant",
		"groq:meta-llama/llama-4-scout-17b-16e-instruct",
		"groq:meta-llama/llama-4-maverick-17b-128e-instruct",
		"groq:openai/gpt-oss-120b",
		"groq:openai/gpt-oss-20b",
		"groq:qwen/qwen3-32b",
		"groq:moonshotai/kimi-k2-instruct-0905",
	},
	// OpenRouter is a router across many providers. Routes ending in
	// ":free" run on free tier quotas (~200 req/day at writing). Routes
	// without ":free" are paid per token. Curated list mixes both. Users
	// can also pass any other openrouter:<route> name.
	"openrouter": {
		"openrouter:nvidia/nemotron-3-super-120b-a12b:free",
		"openrouter:google/gemma-4-31b-it:free",
		"openrouter:inclusionai/ring-2.6-1t:free",
		"openrouter:deepseek/deepseek-v4-flash:free",
		"openrouter:anthropic/claude-opus-4.7",
		"openrouter:deepseek/deepseek-v4-pro",
		"openrouter:moonshotai/kimi-k2.6",
		"openrouter:minimax/minimax-m2.7",
		"openrouter:x-ai/grok-4.20",
		"openrouter:z-ai/glm-5-turbo",
	},
}
