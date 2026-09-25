/** UUID for an interaction answer retry; callers retain it with the unchanged payload. */
export function createInteractionRequestId(): string {
  if (globalThis.crypto?.randomUUID) return globalThis.crypto.randomUUID();
  const hex = () => Math.floor(Math.random() * 0x100000000).toString(16).padStart(8, "0");
  return `${hex()}-${hex().slice(0, 4)}-4${hex().slice(0, 3)}-8${hex().slice(0, 3)}-${hex()}${hex().slice(0, 4)}`;
}
