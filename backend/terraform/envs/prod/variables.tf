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
