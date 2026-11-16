//go:build linux && ishcrypto

#include "textflag.h"

TEXT ·lookupIsh(SB),NOSPLIT,$0-16
 MOVL table+0(FP), BX
 MOVL input+4(FP), SI
 MOVL blocks+8(FP), CX
 MOVL output+12(FP), DI
 PXOR X0, X0
loop:
#define BYTE(pos,reg) MOVBLZX reg, AX; SHLL $4, AX; PXOR ((pos)*4096)(BX)(AX*1), X0
#define WORD(pos) \
 MOVL pos(SI), DX; \
 BYTE(pos,DL); \
 BYTE((pos)+1,DH); \
 SHRL $16, DX; \
 BYTE((pos)+2,DL); \
 BYTE((pos)+3,DH)
 WORD(0)
 WORD(4)
 WORD(8)
 WORD(12)
 SUBL $65536, BX
 ADDL $16, SI
 DECL CX
 JNZ loop
 MOVOU X0, (DI)
 RET
