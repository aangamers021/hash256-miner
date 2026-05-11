#define ROTL64(x, y) rotate((ulong)(x), (ulong)(y))

__constant ulong KECCAK_RC[24] = {
    0x0000000000000001UL, 0x0000000000008082UL, 0x800000000000808aUL,
    0x8000000080008000UL, 0x000000000000808bUL, 0x0000000080000001UL,
    0x8000000080008081UL, 0x8000000000008009UL, 0x000000000000008aUL,
    0x0000000000000088UL, 0x0000000080008009UL, 0x000000008000000aUL,
    0x000000008000808bUL, 0x800000000000008bUL, 0x8000000000008089UL,
    0x8000000000008003UL, 0x8000000000008002UL, 0x8000000000000080UL,
    0x000000000000800aUL, 0x800000008000000aUL, 0x8000000080008081UL,
    0x8000000000008080UL, 0x0000000080000001UL, 0x8000000080008008UL
};

__constant int KECCAK_RO[25] = {
     0,  1, 62, 28, 27,
    36, 44,  6, 55, 20,
     3, 10, 43, 25, 39,
    41, 45, 15, 21,  8,
    18,  2, 61, 56, 14
};

__constant int KECCAK_PI[25] = {
     0, 10, 20,  5, 15,
    16,  1, 11, 21,  6,
     7, 17,  2, 12, 22,
    23,  8, 18,  3, 13,
    14, 24,  9, 19,  4
};

static void keccak_f1600(ulong *A) {
    for (int round = 0; round < 24; round++) {
        ulong C[5];
        for (int x = 0; x < 5; x++) {
            C[x] = A[x] ^ A[x + 5] ^ A[x + 10] ^ A[x + 15] ^ A[x + 20];
        }
        ulong D[5];
        for (int x = 0; x < 5; x++) {
            D[x] = C[(x + 4) % 5] ^ ROTL64(C[(x + 1) % 5], 1);
        }
        for (int i = 0; i < 25; i++) {
            A[i] ^= D[i % 5];
        }

        ulong B[25];
        for (int i = 0; i < 25; i++) {
            B[KECCAK_PI[i]] = ROTL64(A[i], KECCAK_RO[i]);
        }

        for (int y = 0; y < 25; y += 5) {
            ulong b0 = B[y + 0];
            ulong b1 = B[y + 1];
            ulong b2 = B[y + 2];
            ulong b3 = B[y + 3];
            ulong b4 = B[y + 4];
            A[y + 0] = b0 ^ ((~b1) & b2);
            A[y + 1] = b1 ^ ((~b2) & b3);
            A[y + 2] = b2 ^ ((~b3) & b4);
            A[y + 3] = b3 ^ ((~b4) & b0);
            A[y + 4] = b4 ^ ((~b0) & b1);
        }

        A[0] ^= KECCAK_RC[round];
    }
}

static ulong bswap64(ulong v) {
    v = ((v >> 8)  & 0x00FF00FF00FF00FFUL) | ((v & 0x00FF00FF00FF00FFUL) << 8);
    v = ((v >> 16) & 0x0000FFFF0000FFFFUL) | ((v & 0x0000FFFF0000FFFFUL) << 16);
    v = (v >> 32) | (v << 32);
    return v;
}

__kernel void grind(
    __global const ulong *challenge_le,
    __global const ulong *nonce_hi_le,
    __global const ulong *target_be,
    const ulong            counter_base,
    volatile __global uint *found_flag,
    __global ulong *found_nonce_low,
    __global ulong *found_result_be
) {
    size_t gid = get_global_id(0);

    if (*found_flag != 0) return;

    ulong nonce_lo = counter_base + (ulong)gid;

    ulong A[25];
    for (int i = 0; i < 25; i++) A[i] = 0;

    A[0] = challenge_le[0];
    A[1] = challenge_le[1];
    A[2] = challenge_le[2];
    A[3] = challenge_le[3];
    A[4] = nonce_hi_le[0];
    A[5] = nonce_hi_le[1];
    A[6] = nonce_hi_le[2];
    A[7] = bswap64(nonce_lo);

    A[8]  ^= 0x01UL;
    A[16] ^= 0x8000000000000000UL;

    keccak_f1600(A);

    ulong r0 = bswap64(A[0]);
    ulong r1 = bswap64(A[1]);
    ulong r2 = bswap64(A[2]);
    ulong r3 = bswap64(A[3]);

    ulong t0 = target_be[0];
    ulong t1 = target_be[1];
    ulong t2 = target_be[2];
    ulong t3 = target_be[3];

    int lt;
    if (r0 != t0) { lt = (r0 < t0); }
    else if (r1 != t1) { lt = (r1 < t1); }
    else if (r2 != t2) { lt = (r2 < t2); }
    else { lt = (r3 < t3); }

    if (lt) {
        if (atomic_cmpxchg(found_flag, 0u, 1u) == 0u) {
            found_nonce_low[0] = nonce_lo;
            found_result_be[0] = r0;
            found_result_be[1] = r1;
            found_result_be[2] = r2;
            found_result_be[3] = r3;
        }
    }
}
