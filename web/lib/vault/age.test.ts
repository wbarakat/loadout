import { describe, expect, it } from "vitest";
import {
  AgeDecryptError,
  decrypt,
  encryptTo,
  generateKeypair,
  recipientFor,
} from "./age.js";

// The age v1 binary file format starts with this exact magic string.
const AGE_BINARY_HEADER = "age-encryption.org/v1";
const AGE_ARMOR_HEADER = "-----BEGIN AGE ENCRYPTED FILE-----";

// A recipient is "age1" plus 58 bech32 data characters (62 chars total).
const RECIPIENT_RE = /^age1[qpzry9x8gf2tvdw0s3jn54khce6mua7l]{58}$/;
// An identity is "AGE-SECRET-KEY-1" plus 58 bech32 data characters.
const IDENTITY_RE = /^AGE-SECRET-KEY-1[QPZRY9X8GF2TVDW0S3JN54KHCE6MUA7L]{58}$/;

describe("generateKeypair", () => {
  it("returns an X25519 identity and its paired recipient", async () => {
    const keypair = await generateKeypair();
    expect(keypair.identity).toMatch(IDENTITY_RE);
    expect(keypair.recipient).toMatch(RECIPIENT_RE);
  });

  it("returns a different keypair on each call", async () => {
    const a = await generateKeypair();
    const b = await generateKeypair();
    expect(a.identity).not.toBe(b.identity);
    expect(a.recipient).not.toBe(b.recipient);
  });
});

describe("recipientFor", () => {
  it("returns the same recipient generateKeypair paired with the identity", async () => {
    const keypair = await generateKeypair();
    const recipient = await recipientFor(keypair.identity);
    expect(recipient).toBe(keypair.recipient);
  });

  it("rejects a non-X25519 identity with a clear error", async () => {
    await expect(recipientFor("not-an-age-identity")).rejects.toThrow();
  });
});

describe("encryptTo / decrypt round trip", () => {
  it("round-trips the exact plaintext bytes", async () => {
    const keypair = await generateKeypair();
    const plaintext = new TextEncoder().encode("hello, loadout vault");

    const ciphertext = await encryptTo(plaintext, [keypair.recipient]);
    const decrypted = await decrypt(ciphertext, keypair.identity);

    expect(decrypted).toEqual(plaintext);
  });

  it("round-trips arbitrary binary bytes, not just text", async () => {
    const keypair = await generateKeypair();
    const plaintext = new Uint8Array([0, 1, 2, 255, 254, 253, 0, 0, 128]);

    const ciphertext = await encryptTo(plaintext, [keypair.recipient]);
    const decrypted = await decrypt(ciphertext, keypair.identity);

    expect(decrypted).toEqual(plaintext);
  });

  it("emits binary age output, never armored", async () => {
    const keypair = await generateKeypair();
    const plaintext = new TextEncoder().encode("hello, loadout vault");

    const ciphertext = await encryptTo(plaintext, [keypair.recipient]);
    const headerBytes = ciphertext.slice(0, AGE_BINARY_HEADER.length);
    const headerText = new TextDecoder().decode(headerBytes);
    const fullText = new TextDecoder().decode(ciphertext);

    expect(headerText).toBe(AGE_BINARY_HEADER);
    expect(fullText).not.toContain(AGE_ARMOR_HEADER);
  });

  it("encrypts to multiple recipients, decryptable by each", async () => {
    const alice = await generateKeypair();
    const bob = await generateKeypair();
    const plaintext = new TextEncoder().encode("shared secret");

    const ciphertext = await encryptTo(plaintext, [alice.recipient, bob.recipient]);

    expect(await decrypt(ciphertext, alice.identity)).toEqual(plaintext);
    expect(await decrypt(ciphertext, bob.identity)).toEqual(plaintext);
  });

  it("throws AgeDecryptError when decrypting with a different identity", async () => {
    const keypair = await generateKeypair();
    const stranger = await generateKeypair();
    const plaintext = new TextEncoder().encode("hello, loadout vault");

    const ciphertext = await encryptTo(plaintext, [keypair.recipient]);

    await expect(decrypt(ciphertext, stranger.identity)).rejects.toThrow(
      AgeDecryptError,
    );
  });

  it("rejects encryptTo with a non-X25519 recipient", async () => {
    await expect(
      encryptTo(new TextEncoder().encode("x"), ["not-an-age-recipient"]),
    ).rejects.toThrow();
  });

  it("rejects decrypt with a non-X25519 identity", async () => {
    const keypair = await generateKeypair();
    const ciphertext = await encryptTo(
      new TextEncoder().encode("x"),
      [keypair.recipient],
    );
    await expect(decrypt(ciphertext, "not-an-age-identity")).rejects.toThrow();
  });
});
