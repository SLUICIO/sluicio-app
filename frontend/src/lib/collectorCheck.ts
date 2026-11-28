// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// OTelFlow's check of a collector configuration, for a drawer that
// reviews a change before it is sent. When the cell's OTelFlow cannot be
// reached the server answers 502, so a pipeline step fails rather than
// passing unchecked; a person reviewing the change still gets the rest of
// the answer and can send it on the collector's own check.

import { api, type ApiError } from "../api/client";
import type { CollectorValidation } from "../api/types";

export type CollectorCheck = {
  checked: boolean;
  validation?: CollectorValidation;
  kept_by_local?: string[];
  plain_secrets?: string[];
  // Set when OTelFlow is configured but could not check the change. The
  // server then accepts the change only with force.
  check_error?: string;
};

// uncheckedAnswer turns the server's 502 from validate into a review the
// drawer can show, or null for any other failure.
export function uncheckedAnswer(e: unknown): CollectorCheck | null {
  const err = e as ApiError;
  if (err?.status !== 502 || !err.body || typeof err.body !== "object") {
    return null;
  }
  const b = err.body as {
    kept_by_local?: string[];
    plain_secrets?: string[];
    error?: { message?: string };
  };
  return {
    checked: false,
    kept_by_local: b.kept_by_local ?? [],
    plain_secrets: b.plain_secrets ?? [],
    check_error:
      b.error?.message ?? "OTelFlow could not check the configuration",
  };
}

export async function checkCollectorConfig(
  id: string,
  config: string,
): Promise<CollectorCheck> {
  try {
    return await api.validateCollectorConfig(id, config);
  } catch (e) {
    const unchecked = uncheckedAnswer(e);
    if (unchecked) return unchecked;
    throw e;
  }
}
