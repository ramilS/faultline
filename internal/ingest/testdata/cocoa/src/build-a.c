#include <stddef.h>

__attribute__((noinline)) void submit_order(volatile int *order) {
	*order = 1;
}

__attribute__((noinline)) void checkout(volatile int *order) {
	submit_order(order);
}

int main(void) {
	checkout(NULL);
	return 0;
}
