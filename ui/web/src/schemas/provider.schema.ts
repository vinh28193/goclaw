import { z } from "zod";
import { isValidSlug } from "@/lib/slug";
import type { OfflineFormState } from "@/lib/offline-settings-serde";

export const providerCreateSchema = z.object({
  name: z
    .string()
    .min(1, "Required")
    .refine(isValidSlug, "Only lowercase letters, numbers, and hyphens"),
  displayName: z.string().optional(),
  providerType: z.string().min(1, "Required"),
  apiBase: z.string().optional(),
  apiKey: z.string().optional(),
  enabled: z.boolean(),
  // ACP-specific fields
  acpBinary: z.string().optional(),
  acpArgs: z.string().optional(),
  acpIdleTTL: z.string().optional(),
  acpPermMode: z.string().optional(),
  acpWorkDir: z.string().optional(),
  // Offline-specific settings — free-shape UI state, converted to JSONB
  // payload via serializeOfflineSettings at submit time.
  offlineSettings: z.custom<OfflineFormState>().optional(),
});

export type ProviderCreateFormData = z.infer<typeof providerCreateSchema>;
