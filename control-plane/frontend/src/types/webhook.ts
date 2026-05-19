export interface WebhookApiKey {
  id: number;
  key: string;
  label: string;
  is_private: boolean;
  last_used_at?: string | null;
  created_at: string;
}

export interface WebhookLog {
  id: number;
  instance_id: number;
  source_ip: string;
  session_name: string;
  request_bytes: number;
  response_bytes: number;
  status_code: number;
  duration_ms: number;
  error_message?: string;
  key_last4: string;
  is_private: boolean;
  created_at: string;
}

export interface InstanceWebhook {
  instance_uuid: string;
  public_url: string;
  private_url: string;
  keys: WebhookApiKey[];
  recent_logs: WebhookLog[];
}
