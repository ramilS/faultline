#!/bin/sh
# Rebuilds the two-build Cocoa fixture: one crash (submit_order writes through
# NULL) compiled twice. Build B adds a function ahead of submit_order and code
# inside it, and faults deeper into it, so every function sits at a different
# offset and the crash on a different line. Each build yields the dSYM's DWARF
# Mach-O, as an upload carries it, and an envelope of the crash as a release
# build reports it: frames with addresses but no function names.
# Needs Xcode's clang, dsymutil, dwarfdump and nm.
set -eu
cd "$(dirname "$0")"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

build() {
	name=$1 load=$2 event_id=$3 fault=$4
	mkdir -p "$work/$name"
	clang -g -O0 -arch arm64 -c -o "$work/$name/MyApp.o" "src/$name.c"
	clang -arch arm64 -o "$work/$name/MyApp" "$work/$name/MyApp.o"
	dsymutil "$work/$name/MyApp" -o "$work/$name/MyApp.dSYM"
	cp "$work/$name/MyApp.dSYM/Contents/Resources/DWARF/MyApp" "$name.dwarf"
	uuid=$(dwarfdump --uuid "$name.dwarf" | awk '{print tolower($2)}')
	frame() {
		symbol=$(nm "$work/$name/MyApp" | awk -v s="_$1" '$3 == s {print $1}')
		printf '{"package":"/private/var/containers/Bundle/Application/%s/MyApp.app/MyApp","image_addr":"0x%x","symbol_addr":"0x%x","instruction_addr":"0x%x","in_app":true}' \
			"$event_id" "$load" $((load + 0x$symbol - 0x100000000)) $((load + 0x$symbol - 0x100000000 + $2))
	}
	event=$(printf '{"event_id":"%s","platform":"cocoa","level":"fatal","release":"MyApp@1.0.%s","exception":{"values":[{"type":"EXC_BAD_ACCESS","value":"KERN_INVALID_ADDRESS at 0x0","mechanism":{"type":"mach"},"stacktrace":{"frames":[{"package":"/usr/lib/system/libdyld.dylib","image_addr":"0x1a0000000","instruction_addr":"0x1a0001234","in_app":false},%s,%s,%s]}}]},"debug_meta":{"images":[{"type":"macho","debug_id":"%s","image_addr":"0x%x","image_size":32768,"code_file":"MyApp"},{"type":"macho","debug_id":"5b1e8a2c-0000-4000-8000-000000000001","image_addr":"0x1a0000000","image_size":65536,"code_file":"/usr/lib/system/libdyld.dylib"}]}}' \
		"$event_id" "$name" "$(frame main 8)" "$(frame checkout 4)" "$(frame submit_order "$fault")" "$uuid" "$load")
	length=$(printf '%s' "$event" | wc -c | tr -d ' ')
	printf '{"event_id":"%s"}\n{"type":"event","length":%s}\n%s\n' "$event_id" "$length" "$event" > "$name.envelope"
}

build build-a 0x104c00000 7a1c2b3d4e5f40718293a4b5c6d7e8f9 4
build build-b 0x102b00000 8b2d3c4e5f6041829304b5c6d7e8f9a0 0x1c
