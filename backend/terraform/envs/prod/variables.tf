variable "env" {
  type    = string
  default = "prod"
}

variable "region" {
  type    = string
  default = "us-east-1"
}

# Worker reserved concurrency = the global LLM-call ceiling and cost
# circuit-breaker. -1 = unreserved: this NEW account's total Lambda
# concurrency limit is 10 (an even stricter global ceiling) and AWS forbids
# reservations that leave <10 unreserved. Set back to 3-5 after a Service
# Quotas increase on "Concurrent executions".
variable "worker_reserved_concurrency" {
  type    = number
  default = -1
}

variable "daily_scan_cap" {
  type    = number
  default = 20
}

variable "daily_estimate_cap" {
  type    = number
  default = 20
}

# Total free photo scans before the paywall (a one-time grant, no refill).
# Launch value: 10 — enough to feel the camera across a few meals and build a
# diary worth keeping, while the paywall lands while purchase intent is high.
# (Was 1000 during beta, before subscriptions were purchasable.) Subscribers and
# free users alike stay bounded by daily_scan_cap.
variable "free_scan_allowance" {
  type    = number
  default = 10
}

# Free-allowance window in days: the clock starts on the first scan and the
# allowance hard-expires after this many days (a second conversion trigger that
# fires even for light users who never exhaust the count). Launch value: 7 — the
# product design, mirroring the annual plan's 7-day trial. (Was 3650 during beta
# so the scan count, not the clock, was the binding limit.)
variable "free_window_days" {
  type    = number
  default = 7
}

variable "llm_provider" {
  type    = string
  default = "gemini"
}

# Deployed API auth. Static stays available for emergencies; local dev keeps
# static via dev.env regardless.
variable "auth_mode" {
  type    = string
  default = "cognito"
}

# gemini-3.1-pro-preview once the Google billing tier allows it (one var flip).
variable "llm_model_vision" {
  type    = string
  default = "gemini-2.5-flash"
}

variable "llm_model_text" {
  type    = string
  default = "gemini-3.1-flash-lite"
}

variable "entitlement_mode" {
  description = "enforce = free-allowance paywall live; off = everyone full access"
  type        = string
  default     = "enforce"
}

variable "appstore_verify_mode" {
  description = "full = Apple-root JWS verification; insecure_dev = decode-only (Xcode StoreKit testing, refused by the app in prod)"
  type        = string
  default     = "full"
}

variable "apple_verify_mode" {
  description = "Sign-in-with-Apple token verification: full (Apple JWKS) or insecure_dev (decode-only; refused by the app in prod)"
  type        = string
  default     = "full"
}
