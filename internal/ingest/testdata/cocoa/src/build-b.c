#include <stddef.h>

__attribute__((noinline)) int render_banner(volatile int *seen) {
	int total = 0;
	for (int index = 0; index < 64; index++) {
		total += seen[index] * index;
	}
	return total;
}

__attribute__((noinline)) void submit_order(volatile int *order) {
	int retries = 0;
	retries++;

	*order = retries;
}

__attribute__((noinline)) void checkout(volatile int *order) {
	submit_order(order);
}

int main(void) {
	checkout(NULL);
	return 0;
}
