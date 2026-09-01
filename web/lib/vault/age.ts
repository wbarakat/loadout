/**
 * A thin, stable wrapper over the `age-encryption` npm package.
 *
 * This file is the ONLY place in the vault library that imports
 * `age-encryption` directly. Every other module must go through the four
 * functions here. This keeps the third-party API isolated behind one small,
 * stable crypto surface.
 *
 * Scope: X25519 recipients and identities only, binary (unarmored) age v1
 * output only. This matches the Go server (`filippo.io/age`), which never
 * uses passphrase, hybrid post-quantum, tag, or armored age files. A
 * non-X25519 identity or recipient is rejected up front with a clear error.
 */
import * as age from "age-encryption";
import type { AgeKeypair } from "./types.js";

export type { AgeKeypair };

/** Thrown when a ciphertext cannot be decrypted with the given identity. */
export class AgeDecryptError extends Error {
  constructor(message: string, options?: ErrorOptions) {
    super(message, options);
    this.name = "AgeDecryptError";
  }
}

// An age X25519 identity is the literal prefix "AGE-SECRET-KEY-1" followed
// by 58 bech32 data characters (see identity string format in
// filippo.io/age's device.go and age-encryption's recipients.js).
const X25519_IDENTITY_RE = /^AGE-SECRET-KEY-1[QPZRY9X8GF2TVDW0S3JN54KHCE6MUA7L]{58}$/;

// An age X25519 recipient is "age1" followed by 58 bech32 data characters
// (62 chars total). Same charset/shape the Go server validates on
// POST /v1/devices (internal/server/api.go).
const X25519_RECIPIENT_RE = /^age1[qpzry9x8gf2tvdw0s3jn54khce6mua7l]{58}$/;

function assertX25519Identity(identity: string): void {
  if (!X25519_IDENTITY_RE.test(identity)) {
    throw new Error(
      'not an X25519 age identity: expected "AGE-SECRET-KEY-1" followed by 58 bech32 characters',
    );
  }
}

function assertX25519Recipient(recipient: string): void {
  if (!X25519_RECIPIENT_RE.test(recipient)) {
    throw new Error(
      `not an X25519 age recipient: expected "age1" followed by 58 bech32 characters, got "${recipient}"`,
    );
  }
}

/**
 * Generate a new age X25519 keypair.
 *
 * @returns the new identity ("AGE-SECRET-KEY-1...") and its paired
 * recipient ("age1...").
 */
export async function generateKeypair(): Promise<AgeKeypair> {
  const identity = await age.generateX25519Identity();
  const recipient = await age.identityToRecipient(identity);
  return { identity, recipient };
}

/**
 * Compute the age X25519 recipient for a given identity.
 *
 * @param identity an "AGE-SECRET-KEY-1..." identity string.
 * @returns the paired "age1..." recipient.
 * @throws if `identity` is not a well-formed X25519 identity.
 */
export async function recipientFor(identity: string): Promise<string> {
  assertX25519Identity(identity);
  return age.identityToRecipient(identity);
}

/**
 * Decrypt one binary age file with one X25519 identity.
 *
 * @param ciphertext the binary (unarmored) age file to decrypt.
 * @param identity an "AGE-SECRET-KEY-1..." identity string.
 * @returns the exact plaintext bytes.
 * @throws {AgeDecryptError} if `identity` cannot decrypt `ciphertext` — for
 * example because none of the file's recipients match it, or the file is
 * corrupt.
 */
export async function decrypt(
  ciphertext: Uint8Array,
  identity: string,
): Promise<Uint8Array> {
  assertX25519Identity(identity);
  const decrypter = new age.Decrypter();
  decrypter.addIdentity(identity);
  try {
    return await decrypter.decrypt(ciphertext);
  } catch (cause) {
    const detail = cause instanceof Error ? cause.message : String(cause);
    throw new AgeDecryptError(`cannot decrypt age file: ${detail}`, { cause });
  }
}

/**
 * Encrypt plaintext to one or more age X25519 recipients.
 *
 * @param plaintext the bytes to encrypt.
 * @param recipients one or more "age1..." recipient strings.
 * @returns the encrypted file as binary (unarmored) age v1 bytes — the
 * output always starts with the "age-encryption.org/v1" magic string, never
 * "-----BEGIN AGE ENCRYPTED FILE-----".
 * @throws if `recipients` is empty, or any entry is not a well-formed
 * X25519 recipient.
 */
export async function encryptTo(
  plaintext: Uint8Array,
  recipients: string[],
): Promise<Uint8Array> {
  if (recipients.length === 0) {
    throw new Error("encryptTo requires at least one recipient");
  }
  for (const recipient of recipients) {
    assertX25519Recipient(recipient);
  }
  const encrypter = new age.Encrypter();
  for (const recipient of recipients) {
    encrypter.addRecipient(recipient);
  }
  // age.Encrypter.encrypt() returns binary age bytes by default; armoring
  // is a separate, opt-in step (age.armor.encode) that this wrapper never
  // calls.
  return encrypter.encrypt(plaintext);
}
