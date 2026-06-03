/** A Composio OAuth toolkit available to connect (Gmail, Google Analytics, …). */
export interface Toolkit {
  slug: string;
  name: string;
  logo?: string;
}

/** A connection linked to an instance via Composio. */
export interface Connection {
  id: number;
  instance_id: number;
  toolkit_slug: string;
  name: string;
  status: "INITIATED" | "ACTIVE" | "FAILED" | "EXPIRED";
  account_label: string;
  created_at: string;
  updated_at: string;
}

export interface InitiateConnectionResponse {
  connected_account_id: string;
  redirect_url: string;
}

export interface ConfirmConnectionResponse {
  status: Connection["status"];
  /** Present only once the connection becomes ACTIVE and is persisted. */
  connection?: Connection;
}
