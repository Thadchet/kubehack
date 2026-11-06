import { ApiError } from "../../api";

export interface Failure {
  kind:
    | "fieldConflict"
    | "stale"
    | "gone"
    | "invalid"
    | "forbidden"
    | "readOnly"
    | "portInUse"
    | "failed";
  message: string;
  conflicts?: { field: string; message: string }[];
}

export function toFailure(error: unknown): Failure {
  if (error instanceof ApiError) {
    try {
      const parsed = JSON.parse(error.body) as Failure;
      if (parsed.kind) {
        return parsed;
      }
    } catch {
      // Plain-text bodies come from request validation.
    }
    return { kind: "failed", message: error.body };
  }
  return { kind: "failed", message: (error as Error).message };
}
