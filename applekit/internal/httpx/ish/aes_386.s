//go:build linux && ishcrypto

#include "textflag.h"

// The emulator executes every operand as a separate step, so the state lives in
// two 16-byte frame buffers: any byte is one load, without shifts or masks.
// Frame: 0 and 16 state buffers, 32 counter, 36 blocks, 40 source,
// 44 destination, 48 first-round constants. DI holds the iShAES tables:
// round at 0, last at 49152, k0 at 65536, k1 at 65552.
#define WORD(in,out,p0,p1,p2,p3,k) \
 MOVBLZX in+p0(SP), SI; \
 MOVL k(DI)(SI*4), BP; \
 MOVBLZX in+p1(SP), SI; \
 XORL ·te1(SB)(SI*4), BP; \
 MOVBLZX in+p2(SP), SI; \
 XORL ·te2(SB)(SI*4), BP; \
 MOVBLZX in+p3(SP), SI; \
 XORL ·te3(SB)(SI*4), BP; \
 MOVL BP, out(SP)
#define ROUND(in,out,k) \
 WORD(in,out+0,3,6,9,12,k+0); \
 WORD(in,out+4,7,10,13,0,k+1024); \
 WORD(in,out+8,11,14,1,4,k+2048); \
 WORD(in,out+12,15,2,5,8,k+3072)
#define LAST(in,out,p0,p1,p2,p3,k) \
 MOVBLZX in+p0(SP), SI; \
 MOVL k(DI)(SI*4), BP; \
 MOVBLZX in+p1(SP), SI; \
 XORL k+1024(DI)(SI*4), BP; \
 MOVBLZX in+p2(SP), SI; \
 XORL k+2048(DI)(SI*4), BP; \
 MOVBLZX in+p3(SP), SI; \
 XORL k+3072(DI)(SI*4), BP; \
 XORL out(AX), BP; \
 MOVL BP, out(BX)

TEXT ·counterIsh256(SB),NOSPLIT,$64-20
 MOVL tables+0(FP), DI
 MOVL nonce+16(FP), SI
 MOVL 0(SI), AX
 BSWAPL AX
 XORL 65536(DI), AX
 MOVL AX, 0(SP)
 MOVL 4(SI), AX
 BSWAPL AX
 XORL 65540(DI), AX
 MOVL AX, 4(SP)
 MOVL 8(SI), AX
 BSWAPL AX
 XORL 65544(DI), AX
 MOVL AX, 8(SP)
 MOVL 12(SI), AX
 BSWAPL AX
 MOVL AX, 32(SP)
 MOVBLZX 3(SP), SI
 MOVL ·te0(SB)(SI*4), BP
 MOVBLZX 6(SP), SI
 XORL ·te1(SB)(SI*4), BP
 MOVBLZX 9(SP), SI
 XORL ·te2(SB)(SI*4), BP
 XORL 65552(DI), BP
 MOVL BP, 48(SP)
 MOVBLZX 0(SP), SI
 MOVL ·te3(SB)(SI*4), BP
 MOVBLZX 7(SP), SI
 XORL ·te0(SB)(SI*4), BP
 MOVBLZX 10(SP), SI
 XORL ·te1(SB)(SI*4), BP
 XORL 65556(DI), BP
 MOVL BP, 52(SP)
 MOVBLZX 1(SP), SI
 MOVL ·te2(SB)(SI*4), BP
 MOVBLZX 4(SP), SI
 XORL ·te3(SB)(SI*4), BP
 MOVBLZX 11(SP), SI
 XORL ·te0(SB)(SI*4), BP
 XORL 65560(DI), BP
 MOVL BP, 56(SP)
 MOVBLZX 2(SP), SI
 MOVL ·te1(SB)(SI*4), BP
 MOVBLZX 5(SP), SI
 XORL ·te2(SB)(SI*4), BP
 MOVBLZX 8(SP), SI
 XORL ·te3(SB)(SI*4), BP
 XORL 65564(DI), BP
 MOVL BP, 60(SP)
 MOVL count+12(FP), SI
 MOVL SI, 36(SP)
 MOVL source+8(FP), SI
 MOVL SI, 40(SP)
 MOVL destination+4(FP), SI
 MOVL SI, 44(SP)
block:
 MOVL 32(SP), DX
 XORL 65548(DI), DX
 MOVBLZX DL, SI
 MOVL ·te3(SB)(SI*4), BP
 XORL 48(SP), BP
 MOVL BP, 16(SP)
 MOVBLZX DH, SI
 MOVL ·te2(SB)(SI*4), BP
 XORL 52(SP), BP
 MOVL BP, 20(SP)
 SHRL $16, DX
 MOVBLZX DL, SI
 MOVL ·te1(SB)(SI*4), BP
 XORL 56(SP), BP
 MOVL BP, 24(SP)
 MOVBLZX DH, SI
 MOVL ·te0(SB)(SI*4), BP
 XORL 60(SP), BP
 MOVL BP, 28(SP)
 ROUND(16,0,0)
 ROUND(0,16,4096)
 ROUND(16,0,8192)
 ROUND(0,16,12288)
 ROUND(16,0,16384)
 ROUND(0,16,20480)
 ROUND(16,0,24576)
 ROUND(0,16,28672)
 ROUND(16,0,32768)
 ROUND(0,16,36864)
 ROUND(16,0,40960)
 ROUND(0,16,45056)
 MOVL 40(SP), AX
 MOVL 44(SP), BX
 LAST(16,0,3,6,9,12,49152)
 LAST(16,4,7,10,13,0,53248)
 LAST(16,8,11,14,1,4,57344)
 LAST(16,12,15,2,5,8,61440)
 ADDL $16, 40(SP)
 ADDL $16, 44(SP)
 ADDL $1, 32(SP)
 SUBL $1, 36(SP)
 JNZ block
 RET
