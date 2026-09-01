/**
 * Shared types for the vault library.
 *
 * Put a type here only if more than one module needs it. Keep
 * module-specific types next to the module that owns them.
 */

/**
 * A raw age X25519 identity and its paired recipient.
 */
export interface AgeKeypair {
  /** An age X25519 identity string, for example "AGE-SECRET-KEY-1...". */
  identity: string;
  /** An age X25519 recipient string, for example "age1...". */
  recipient: string;
}
