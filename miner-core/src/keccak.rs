use anyhow::{bail, Context, Result};
use sha3::{Digest, Keccak256};

pub const PREIMAGE_LEN: usize = 64;

pub fn parse_hex32(s: &str) -> Result<[u8; 32]> {
    let s = s.trim_start_matches("0x");
    if s.len() != 64 {
        bail!("expected 32-byte hex (64 chars), got {}", s.len());
    }
    let mut out = [0u8; 32];
    hex::decode_to_slice(s, &mut out).context("invalid hex")?;
    Ok(out)
}

pub fn parse_hex_prefix24(s: &str) -> Result<[u8; 24]> {
    let s = s.trim_start_matches("0x");
    if s.len() < 48 {
        bail!("expected at least 24-byte prefix hex, got {}", s.len());
    }
    let mut out = [0u8; 24];
    hex::decode_to_slice(&s[..48], &mut out).context("invalid hex")?;
    Ok(out)
}

pub fn to_hex32(bytes: &[u8; 32]) -> String {
    let mut s = String::with_capacity(66);
    s.push_str("0x");
    s.push_str(&hex::encode(bytes));
    s
}

#[inline]
pub fn keccak256_64(challenge: &[u8; 32], nonce: &[u8; 32]) -> [u8; 32] {
    let mut h = Keccak256::new();
    h.update(challenge);
    h.update(nonce);
    h.finalize().into()
}

#[inline]
pub fn is_below_target_be(result: &[u8; 32], target: &[u8; 32]) -> bool {
    for i in 0..32 {
        if result[i] != target[i] {
            return result[i] < target[i];
        }
    }
    false
}

pub fn verify(challenge: &[u8; 32], nonce: &[u8; 32], target: &[u8; 32]) -> bool {
    let r = keccak256_64(challenge, nonce);
    is_below_target_be(&r, target)
}

#[derive(Clone, Copy)]
pub struct Target {
    pub be: [u8; 32],
}

impl Target {
    pub fn from_be(b: [u8; 32]) -> Self {
        Self { be: b }
    }
}

#[derive(Clone, Copy)]
pub struct NonceBuilder {
    pub prefix: [u8; 24],
}

impl NonceBuilder {
    pub fn new(prefix: [u8; 24]) -> Self {
        Self { prefix }
    }

    #[inline]
    pub fn build(&self, counter: u64) -> [u8; 32] {
        let mut out = [0u8; 32];
        out[..24].copy_from_slice(&self.prefix);
        out[24..].copy_from_slice(&counter.to_be_bytes());
        out
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn keccak_matches_known_vector() {
        let challenge = [0u8; 32];
        let nonce = [0u8; 32];
        let got = keccak256_64(&challenge, &nonce);
        let expected = hex::decode(
            "f490de2920c8a35fabeb13208852aa28c76f9be9b03a4dd2b3c075f7a26923b4",
        )
        .unwrap();
        assert_eq!(got[..], expected[..]);
    }

    #[test]
    fn target_comparison_big_endian() {
        let mut a = [0u8; 32];
        let mut b = [0u8; 32];
        a[0] = 0x01;
        b[0] = 0x02;
        assert!(is_below_target_be(&a, &b));
        assert!(!is_below_target_be(&b, &a));
        a[0] = 0;
        a[31] = 5;
        b[0] = 0;
        b[31] = 10;
        assert!(is_below_target_be(&a, &b));
    }
}
