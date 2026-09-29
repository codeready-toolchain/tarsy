/** Model label with effort appended only when the effort was stored. */
export function formatModelWithEffort(model?: string | null, effort?: string | null): string {
  if (!model) return '';
  if (!effort) return model;
  return `${model} (${effort})`;
}
