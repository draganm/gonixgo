/* asm_seven returns 7. One file serves arm64 and x86-64, Mach-O and ELF. */
#if defined(__APPLE__)
#define SYM(x) _##x
#else
#define SYM(x) x
#endif

	.text
	.globl SYM(asm_seven)
#if defined(__aarch64__)
	.p2align 2
SYM(asm_seven):
	mov w0, #7
	ret
#elif defined(__x86_64__)
SYM(asm_seven):
	movl $7, %eax
	ret
#else
#error "seven.S supports arm64 and x86-64 only"
#endif

#if defined(__linux__) && defined(__ELF__)
	.section .note.GNU-stack,"",%progbits
#endif
