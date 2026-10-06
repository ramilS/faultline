// Package proguard parses R8 and ProGuard mapping files and retraces obfuscated
// JVM stack frames to their original class, method and line.
//
// Mapping text is untrusted upload content. Parsing is a single streaming pass
// with a bounded line length, and it keeps only the classes a caller asks for,
// so a read-time retrace of a few frames never holds a whole mapping in memory.
package proguard
