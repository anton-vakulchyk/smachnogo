variable "env" {
  type    = string
  default = "dev"
}

variable "region" {
  type    = string
  default = "us-east-1"
}

# Worker reserved concurrency = the global Claude-call ceiling and cost
# circuit-breaker. Keep small.
variable "worker_reserved_concurrency" {
  type    = number
  default = 3
}

variable "daily_scan_cap" {
  type    = number
  default = 20
}

variable "daily_estimate_cap" {
  type    = number
  default = 20
}

variable "llm_model_vision" {
  type    = string
  default = "claude-opus-4-8"
}

variable "llm_model_text" {
  type    = string
  default = "claude-haiku-4-5"
}
