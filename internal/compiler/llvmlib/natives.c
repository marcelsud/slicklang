#define _GNU_SOURCE
#include <ctype.h>
#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <inttypes.h>
#include <limits.h>
#include <math.h>
#include <locale.h>
#include <netdb.h>
#include <netinet/in.h>
#include <poll.h>
#include <pthread.h>
#include <signal.h>
#include <stdarg.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <sys/wait.h>
#include <time.h>
#include <unistd.h>
#include <wchar.h>
#include <wctype.h>
#ifdef SLICK_HAS_SQLITE
#include <sqlite3.h>
#endif
#ifdef SLICK_HAS_CURL
#include <curl/curl.h>
#endif

typedef struct slick_value { int32_t kind; int32_t flags; int64_t bits; } slick_value;
typedef struct slick_outcome { int32_t code; int32_t pad; slick_value value; } slick_outcome;
typedef struct slick_ctx { volatile int cancelled; void *scope; } slick_ctx;
typedef slick_outcome (*slick_fn)(void *ctx, slick_value *args);

enum { SLICK_OK = 0, SLICK_THROW = 1, SLICK_CANCEL = 5 };

slick_value slick_rt_bool(int32_t v);
slick_value slick_rt_int(int64_t v);
slick_value slick_rt_float(double v);
slick_value slick_rt_string(const char *s, int64_t len);
slick_value slick_rt_bytes(const void *data, int64_t len);
slick_value slick_rt_array(int32_t kind, int64_t n, slick_value *items);
slick_value slick_rt_optional(int present, slick_value payload);
slick_value slick_rt_none(void);
slick_value slick_rt_some(slick_value payload);
slick_value slick_rt_result(int ok, slick_value payload);
slick_value slick_rt_class(int32_t type_id, int32_t n, slick_value *fields);
slick_value slick_rt_union(int32_t type_id, int32_t tag, int32_t n, slick_value *fields);
slick_value slick_rt_map(int64_t n, slick_value *keys, slick_value *vals);
slick_value slick_rt_empty_map(void);
slick_value slick_rt_map_get(slick_value map, slick_value key);
slick_value slick_rt_map_with(slick_value map, slick_value key, slick_value val);
int32_t slick_rt_result_ok(slick_value v);
slick_value slick_rt_result_payload(slick_value v);
int32_t slick_rt_optional_present(slick_value v);
slick_value slick_rt_optional_value(slick_value v);
slick_value slick_rt_field(slick_value obj, int32_t index);
void slick_rt_set_field(slick_value obj, int32_t index, slick_value value);
int32_t slick_rt_find_field(int32_t type_id, const char *name);
int32_t slick_rt_type_id(const char *name);
int32_t slick_rt_union_id(const char *name);
int32_t slick_rt_class_type(slick_value obj);
int32_t slick_rt_type_field_count(int32_t id);
const char *slick_rt_type_field_name(int32_t id, int32_t i);
slick_value slick_rt_array_index(slick_value a, int64_t index);
int64_t slick_rt_array_len(slick_value a);
slick_outcome slick_rt_check_cancel(slick_ctx *ctx);
slick_value slick_rt_format(slick_value v);
slick_value slick_rt_iface(int32_t type_id, slick_value recv, int32_t n, slick_fn *vtable);
slick_outcome slick_rt_iface_call(slick_ctx *ctx, slick_value iface, int32_t slot, int32_t argc, slick_value *args);
slick_value slick_rt_iface_receiver(slick_value value);
slick_ctx *slick_rt_root_ctx(void);
static slick_outcome slick_ok(slick_value v) { slick_outcome o; o.code = SLICK_OK; o.pad = 0; o.value = v; return o; }
static slick_outcome slick_throw(slick_value v) { slick_outcome o; o.code = SLICK_THROW; o.pad = 0; o.value = v; return o; }

static const char *sv_str(slick_value v) {
    slick_value s = v.kind == 4 ? v : slick_rt_format(v);
    typedef struct { int64_t len; uint8_t *data; } bytes;
    bytes *b = (bytes *)(uintptr_t)s.bits;
    return (b && b->data) ? (const char *)b->data : "";
}
static int64_t sv_len(slick_value v) {
    typedef struct { int64_t len; uint8_t *data; } bytes;
    bytes *b = (bytes *)(uintptr_t)v.bits;
    return b ? b->len : 0;
}
static const uint8_t *sv_data(slick_value v) {
    typedef struct { int64_t len; uint8_t *data; } bytes;
    bytes *b = (bytes *)(uintptr_t)v.bits;
    return b ? b->data : NULL;
}

static slick_value class_field(slick_value obj, const char *name) {
    int id = slick_rt_class_type(obj);
    int i = slick_rt_find_field(id, name);
    return i >= 0 ? slick_rt_field(obj, i) : slick_rt_none();
}

static slick_value make_class(const char *type, ...) {
    int id = slick_rt_type_id(type);
    int n = slick_rt_type_field_count(id);
    slick_value *fields = n ? calloc((size_t)n, sizeof(slick_value)) : NULL;
    va_list ap;
    va_start(ap, type);
    for (;;) {
        const char *name = va_arg(ap, const char *);
        if (!name) break;
        slick_value val = va_arg(ap, slick_value);
        for (int i = 0; i < n; i++) {
            if (strcmp(slick_rt_type_field_name(id, i), name) == 0) {
                fields[i] = val;
                break;
            }
        }
    }
    va_end(ap);
    return slick_rt_class(id, n, fields);
}

static void set_resource(slick_value obj, void *p) {
    typedef struct slick_class { int32_t type_id; int32_t field_count; void *resource; slick_value *fields; } slick_class;
    slick_class *c = (slick_class *)(uintptr_t)obj.bits;
    if (c) c->resource = p;
}
static void *get_resource(slick_value obj) {
    typedef struct slick_class { int32_t type_id; int32_t field_count; void *resource; slick_value *fields; } slick_class;
    obj = slick_rt_iface_receiver(obj);
    slick_class *c = (slick_class *)(uintptr_t)obj.bits;
    return c ? c->resource : NULL;
}

static int cancelled(slick_ctx *ctx) {
    return ctx && ctx->cancelled;
}

static int utf8_decode_one(const uint8_t *p, int64_t n, int32_t *value, int *width) {
    if (n <= 0) return 0;
    uint8_t first = p[0];
    if (first < 0x80) {
        *value = first; *width = 1; return 1;
    }
    if (first >= 0xc2 && first <= 0xdf && n >= 2 && (p[1] & 0xc0) == 0x80) {
        *value = ((first & 0x1f) << 6) | (p[1] & 0x3f); *width = 2; return 1;
    }
    if (first >= 0xe0 && first <= 0xef && n >= 3 &&
        (p[1] & 0xc0) == 0x80 && (p[2] & 0xc0) == 0x80 &&
        !(first == 0xe0 && p[1] < 0xa0) && !(first == 0xed && p[1] >= 0xa0)) {
        *value = ((first & 0x0f) << 12) | ((p[1] & 0x3f) << 6) | (p[2] & 0x3f);
        *width = 3; return 1;
    }
    if (first >= 0xf0 && first <= 0xf4 && n >= 4 &&
        (p[1] & 0xc0) == 0x80 && (p[2] & 0xc0) == 0x80 && (p[3] & 0xc0) == 0x80 &&
        !(first == 0xf0 && p[1] < 0x90) && !(first == 0xf4 && p[1] >= 0x90)) {
        *value = ((first & 0x07) << 18) | ((p[1] & 0x3f) << 12) | ((p[2] & 0x3f) << 6) | (p[3] & 0x3f);
        *width = 4; return 1;
    }
    return 0;
}

static slick_value utf8_valid(const uint8_t *p, int64_t n) {
    int64_t offset = 0;
    while (offset < n) {
        int32_t value;
        int width;
        if (!utf8_decode_one(p + offset, n - offset, &value, &width)) return slick_rt_bool(0);
        offset += width;
    }
    return slick_rt_bool(1);
}

slick_outcome slick_nat_bytes_from_utf8(slick_ctx *ctx, slick_value *args) {
    (void)ctx;
    return slick_ok(slick_rt_bytes(sv_str(args[0]), sv_len(args[0])));
}
slick_outcome slick_nat_bytes_to_utf8(slick_ctx *ctx, slick_value *args) {
    (void)ctx;
    if (!utf8_valid(sv_data(args[0]), sv_len(args[0])).bits) {
        return slick_ok(slick_rt_result(0, make_class("std.bytes.Utf8Failure", "Message", slick_rt_string("invalid UTF-8", -1), NULL)));
    }
    return slick_ok(slick_rt_result(1, slick_rt_string((const char *)sv_data(args[0]), sv_len(args[0]))));
}
slick_outcome slick_nat_bytes_length(slick_ctx *ctx, slick_value *args) {
    (void)ctx;
    return slick_ok(slick_rt_int(sv_len(args[0])));
}
slick_outcome slick_nat_bytes_at(slick_ctx *ctx, slick_value *args) {
    (void)ctx;
    int64_t i = args[1].bits;
    if (i < 0 || i >= sv_len(args[0])) return slick_ok(slick_rt_none());
    return slick_ok(slick_rt_some(slick_rt_int(sv_data(args[0])[i])));
}
slick_outcome slick_nat_bytes_concat(slick_ctx *ctx, slick_value *args) {
    (void)ctx;
    int64_t n = slick_rt_array_len(args[0]), total = 0;
    for (int64_t i = 0; i < n; i++) total += sv_len(slick_rt_array_index(args[0], i));
    uint8_t *buf = malloc((size_t)total + 1);
    int64_t off = 0;
    for (int64_t i = 0; i < n; i++) {
        slick_value b = slick_rt_array_index(args[0], i);
        memcpy(buf + off, sv_data(b), (size_t)sv_len(b));
        off += sv_len(b);
    }
    slick_value out = slick_rt_bytes(buf, total);
    free(buf);
    return slick_ok(out);
}
slick_outcome slick_nat_bytes_slice(slick_ctx *ctx, slick_value *args) {
    (void)ctx;
    int64_t start = args[1].bits, end = args[2].bits, n = sv_len(args[0]);
    if (start < 0 || end < start || end > n) {
        return slick_ok(slick_rt_result(0, make_class("std.bytes.BoundsFailure",
            "Start", slick_rt_int(start), "End", slick_rt_int(end), "Length", slick_rt_int(n),
            "Message", slick_rt_string("slice bounds out of range", -1), NULL)));
    }
    return slick_ok(slick_rt_result(1, slick_rt_bytes(sv_data(args[0]) + start, end - start)));
}
slick_outcome slick_nat_bytes_from_values(slick_ctx *ctx, slick_value *args) {
    (void)ctx;
    int64_t n = slick_rt_array_len(args[0]);
    uint8_t *buf = malloc((size_t)n + 1);
    for (int64_t i = 0; i < n; i++) {
        int64_t v = slick_rt_array_index(args[0], i).bits;
        if (v < 0 || v > 255) {
            free(buf);
            return slick_ok(slick_rt_result(0, make_class("std.bytes.ValueFailure",
                "Index", slick_rt_int(i), "Value", slick_rt_int(v),
                "Message", slick_rt_string("byte value must be between 0 and 255", -1), NULL)));
        }
        buf[i] = (uint8_t)v;
    }
    slick_value out = slick_rt_bytes(buf, n);
    free(buf);
    return slick_ok(slick_rt_result(1, out));
}

slick_outcome slick_nat_utf8_decode_at(slick_ctx *ctx, slick_value *args) {
    (void)ctx;
    int64_t i = args[1].bits, n = sv_len(args[0]);
    if (i < 0 || i >= n) {
        return slick_ok(slick_rt_result(0, make_class("std.utf8.Failure",
            "Index", slick_rt_int(i), "Message", slick_rt_string("byte index out of range", -1), NULL)));
    }
    int32_t cp;
    int width;
    if (!utf8_decode_one(sv_data(args[0]) + i, n - i, &cp, &width)) {
        return slick_ok(slick_rt_result(0, make_class("std.utf8.Failure",
            "Index", slick_rt_int(i), "Message", slick_rt_string("invalid UTF-8 encoding", -1), NULL)));
    }
    return slick_ok(slick_rt_result(1, make_class("std.utf8.DecodedRune",
        "Value", slick_rt_int(cp), "Width", slick_rt_int(width), NULL)));
}

static int valid_rune(int64_t v) {
    return v >= 0 && v <= 0x10ffff && !(v >= 0xd800 && v <= 0xdfff);
}
slick_outcome slick_nat_unicode_is_letter(slick_ctx *c, slick_value *a) {
    (void)c; setlocale(LC_CTYPE, "");
    return slick_ok(slick_rt_bool(valid_rune(a[0].bits) && iswalpha((wint_t)a[0].bits)));
}
slick_outcome slick_nat_unicode_is_digit(slick_ctx *c, slick_value *a) {
    (void)c;
    int64_t value = a[0].bits;
    int digit = iswdigit((wint_t)value) || (value >= 0x0660 && value <= 0x0669);
    return slick_ok(slick_rt_bool(valid_rune(value) && digit));
}
slick_outcome slick_nat_unicode_is_space(slick_ctx *c, slick_value *a) {
    (void)c; setlocale(LC_CTYPE, "");
    return slick_ok(slick_rt_bool(valid_rune(a[0].bits) && iswspace((wint_t)a[0].bits)));
}
slick_outcome slick_nat_unicode_is_upper(slick_ctx *c, slick_value *a) {
    (void)c; setlocale(LC_CTYPE, "");
    return slick_ok(slick_rt_bool(valid_rune(a[0].bits) && iswupper((wint_t)a[0].bits)));
}

slick_outcome slick_nat_parse_int(slick_ctx *c, slick_value *a) {
    (void)c;
    const char *text = sv_str(a[0]);
    int64_t length = sv_len(a[0]);
    char *end = NULL;
    errno = 0;
    long long v = strtoll(text, &end, 10);
    if (end == text || end != text + length || (length > 0 && isspace((unsigned char)text[0]))) {
        return slick_ok(slick_rt_result(0, make_class("std.convert.Failure", "Target", slick_rt_string("int", -1), "Message", slick_rt_string("invalid base-10 integer", -1), NULL)));
    }
    if (errno == ERANGE) {
        return slick_ok(slick_rt_result(0, make_class("std.convert.Failure", "Target", slick_rt_string("int", -1), "Message", slick_rt_string("integer out of range", -1), NULL)));
    }
    return slick_ok(slick_rt_result(1, slick_rt_int(v)));
}
slick_outcome slick_nat_parse_float(slick_ctx *c, slick_value *a) {
    (void)c;
    const char *text = sv_str(a[0]);
    int64_t length = sv_len(a[0]);
    char *end = NULL;
    errno = 0;
    double v = strtod(text, &end);
    if (end == text || end != text + length || (length > 0 && isspace((unsigned char)text[0]))) {
        return slick_ok(slick_rt_result(0, make_class("std.convert.Failure", "Target", slick_rt_string("float", -1), "Message", slick_rt_string("invalid floating-point number", -1), NULL)));
    }
    if (errno == ERANGE) {
        return slick_ok(slick_rt_result(0, make_class("std.convert.Failure", "Target", slick_rt_string("float", -1), "Message", slick_rt_string("floating-point value out of range", -1), NULL)));
    }
    if (!isfinite(v)) {
        return slick_ok(slick_rt_result(0, make_class("std.convert.Failure", "Target", slick_rt_string("float", -1), "Message", slick_rt_string("invalid floating-point number", -1), NULL)));
    }
    return slick_ok(slick_rt_result(1, slick_rt_float(v)));
}
slick_outcome slick_nat_int_to_string(slick_ctx *c, slick_value *a) {
    (void)c;
    char buf[32];
    snprintf(buf, sizeof(buf), "%" PRId64, a[0].bits);
    return slick_ok(slick_rt_string(buf, -1));
}
slick_outcome slick_nat_float_to_string(slick_ctx *c, slick_value *a) {
    (void)c;
    double v;
    memcpy(&v, &a[0].bits, sizeof(double));
    if (!isfinite(v)) return slick_throw(slick_rt_string("std.convert.FloatToString cannot format non-finite float", -1));
    char buf[64];
    snprintf(buf, sizeof(buf), "%.17g", v);
    return slick_ok(slick_rt_string(buf, -1));
}

slick_outcome slick_nat_math_div(slick_ctx *c, slick_value *a) {
    (void)c;
    if (a[1].bits == 0) {
        return slick_ok(slick_rt_result(0, make_class("std.math.ArithmeticFailure", "Operation", slick_rt_string("Divide", -1), "Kind", slick_rt_string("DivisionByZero", -1), "Message", slick_rt_string("division by zero", -1), NULL)));
    }
    if (a[0].bits == INT64_MIN && a[1].bits == -1) {
        return slick_ok(slick_rt_result(0, make_class("std.math.ArithmeticFailure", "Operation", slick_rt_string("Divide", -1), "Kind", slick_rt_string("Overflow", -1), "Message", slick_rt_string("integer division overflow", -1), NULL)));
    }
    return slick_ok(slick_rt_result(1, slick_rt_int(a[0].bits / a[1].bits)));
}
slick_outcome slick_nat_math_rem(slick_ctx *c, slick_value *a) {
    (void)c;
    if (a[1].bits == 0) {
        return slick_ok(slick_rt_result(0, make_class("std.math.ArithmeticFailure", "Operation", slick_rt_string("Remainder", -1), "Kind", slick_rt_string("DivisionByZero", -1), "Message", slick_rt_string("division by zero", -1), NULL)));
    }
    if (a[0].bits == INT64_MIN && a[1].bits == -1) return slick_ok(slick_rt_result(1, slick_rt_int(0)));
    return slick_ok(slick_rt_result(1, slick_rt_int(a[0].bits % a[1].bits)));
}

slick_outcome slick_nat_env_get(slick_ctx *c, slick_value *a) {
    (void)c;
    const char *v = getenv(sv_str(a[0]));
    return slick_ok(v ? slick_rt_some(slick_rt_string(v, -1)) : slick_rt_none());
}
slick_outcome slick_nat_env_set(slick_ctx *c, slick_value *a) {
    (void)c;
    if (setenv(sv_str(a[0]), sv_str(a[1]), 1) != 0) {
        return slick_ok(slick_rt_result(0, make_class("std.env.Failure", "Operation", slick_rt_string("Set", -1), "Name", a[0], "Message", slick_rt_string(strerror(errno), -1), NULL)));
    }
    return slick_ok(slick_rt_result(1, (slick_value){0, 0, 0}));
}
slick_outcome slick_nat_env_unset(slick_ctx *c, slick_value *a) {
    (void)c;
    if (unsetenv(sv_str(a[0])) != 0) {
        return slick_ok(slick_rt_result(0, make_class("std.env.Failure", "Operation", slick_rt_string("Unset", -1), "Name", a[0], "Message", slick_rt_string(strerror(errno), -1), NULL)));
    }
    return slick_ok(slick_rt_result(1, (slick_value){0, 0, 0}));
}

static slick_value fs_fail(const char *op, slick_value path, const char *msg) {
    return make_class("std.fs.Failure", "Operation", slick_rt_string(op, -1), "Path", path, "Message", slick_rt_string(msg, -1), NULL);
}

static int fs_path_valid(slick_value path) {
    return memchr(sv_str(path), 0, (size_t)sv_len(path)) == NULL;
}

static char *fs_message_n(const char *verb, const char *path, size_t path_len, int code) {
    const char *raw = strerror(code);
    size_t verb_len = strlen(verb), raw_len = strlen(raw);
    char *message = malloc(verb_len + 1 + path_len + 2 + raw_len + 1);
    memcpy(message, verb, verb_len);
    message[verb_len] = ' ';
    memcpy(message + verb_len + 1, path, path_len);
    memcpy(message + verb_len + 1 + path_len, ": ", 2);
    memcpy(message + verb_len + 3 + path_len, raw, raw_len + 1);
    message[verb_len + 3 + path_len] = (char)tolower((unsigned char)message[verb_len + 3 + path_len]);
    return message;
}

static char *fs_message_value(const char *verb, slick_value path, int code) {
    return fs_message_n(verb, sv_str(path), (size_t)sv_len(path), code);
}

static char *fs_message_text(const char *verb, const char *path, int code) {
    return fs_message_n(verb, path, strlen(path), code);
}

static slick_value fs_fail_errno_value(const char *op, slick_value path, const char *verb, int code) {
    size_t length = strlen(verb) + 1 + (size_t)sv_len(path) + 2 + strlen(strerror(code));
    char *message = fs_message_n(verb, sv_str(path), (size_t)sv_len(path), code);
    return make_class("std.fs.Failure", "Operation", slick_rt_string(op, -1), "Path", path,
        "Message", slick_rt_string(message, (int64_t)length), NULL);
}

slick_outcome slick_nat_fs_read_text(slick_ctx *ctx, slick_value *a) {
    if (cancelled(ctx)) return slick_ok(slick_rt_result(0, fs_fail("ReadText", a[0], "operation cancelled")));
    if (!fs_path_valid(a[0])) return slick_ok(slick_rt_result(0, fs_fail_errno_value("ReadText", a[0], "open", EINVAL)));
    FILE *f = fopen(sv_str(a[0]), "rb");
    if (!f) return slick_ok(slick_rt_result(0, fs_fail("ReadText", a[0], fs_message_value("open", a[0], errno))));
    fseek(f, 0, SEEK_END);
    long n = ftell(f);
    fseek(f, 0, SEEK_SET);
    if (n < 0) n = 0;
    char *buf = malloc((size_t)n + 1);
    size_t got = fread(buf, 1, (size_t)n, f);
    fclose(f);
    if (!utf8_valid((uint8_t *)buf, (int64_t)got).bits) {
        free(buf);
        return slick_ok(slick_rt_result(0, fs_fail("ReadText", a[0], "invalid UTF-8")));
    }
    slick_value out = slick_rt_string(buf, (int64_t)got);
    free(buf);
    return slick_ok(slick_rt_result(1, out));
}
slick_outcome slick_nat_fs_write_text(slick_ctx *ctx, slick_value *a) {
    if (cancelled(ctx)) return slick_ok(slick_rt_result(0, fs_fail("WriteText", a[0], "operation cancelled")));
    if (!fs_path_valid(a[0])) return slick_ok(slick_rt_result(0, fs_fail_errno_value("WriteText", a[0], "open", EINVAL)));
    FILE *f = fopen(sv_str(a[0]), "wb");
    if (!f) return slick_ok(slick_rt_result(0, fs_fail("WriteText", a[0], fs_message_value("open", a[0], errno))));
    fwrite(sv_str(a[1]), 1, (size_t)sv_len(a[1]), f);
    fclose(f);
    return slick_ok(slick_rt_result(1, (slick_value){0, 0, 0}));
}
slick_outcome slick_nat_fs_exists(slick_ctx *c, slick_value *a) {
    (void)c;
    if (!fs_path_valid(a[0])) return slick_ok(slick_rt_result(0, fs_fail_errno_value("Exists", a[0], "stat", EINVAL)));
    struct stat st;
    if (stat(sv_str(a[0]), &st) == 0) return slick_ok(slick_rt_result(1, slick_rt_bool(1)));
    if (errno == ENOENT) return slick_ok(slick_rt_result(1, slick_rt_bool(0)));
    return slick_ok(slick_rt_result(0, fs_fail("Exists", a[0], fs_message_value("stat", a[0], errno))));
}
slick_outcome slick_nat_fs_mkdir(slick_ctx *c, slick_value *a) {
    (void)c;
    if (!fs_path_valid(a[0])) return slick_ok(slick_rt_result(0, fs_fail_errno_value("CreateDirectoryAll", a[0], "mkdir", EINVAL)));
    char *path = strdup(sv_str(a[0]));
    for (char *p = path + 1; ; p++) {
        if (*p != '/' && *p != 0) continue;
        char saved = *p;
        *p = 0;
        if (mkdir(path, 0777) != 0 && errno != EEXIST) {
            slick_value fail = slick_rt_result(0, fs_fail("CreateDirectoryAll", a[0], fs_message_text("mkdir", path, errno)));
            free(path);
            return slick_ok(fail);
        }
        struct stat st;
        if (stat(path, &st) != 0 || !S_ISDIR(st.st_mode)) {
            slick_value fail = slick_rt_result(0, fs_fail("CreateDirectoryAll", a[0], fs_message_text("mkdir", path, ENOTDIR)));
            free(path);
            return slick_ok(fail);
        }
        *p = saved;
        if (saved == 0) break;
    }
    free(path);
    return slick_ok(slick_rt_result(1, (slick_value){0, 0, 0}));
}
slick_outcome slick_nat_fs_remove(slick_ctx *c, slick_value *a) {
    (void)c;
    if (!fs_path_valid(a[0])) return slick_ok(slick_rt_result(0, fs_fail_errno_value("Remove", a[0], "remove", EINVAL)));
    if (remove(sv_str(a[0])) != 0) return slick_ok(slick_rt_result(0, fs_fail("Remove", a[0], fs_message_value("remove", a[0], errno))));
    return slick_ok(slick_rt_result(1, (slick_value){0, 0, 0}));
}

static int name_cmp(const void *a, const void *b) {
    return strcmp(*(char *const *)a, *(char *const *)b);
}
slick_outcome slick_nat_fs_read_dir(slick_ctx *c, slick_value *a) {
    (void)c;
    if (!fs_path_valid(a[0])) return slick_ok(slick_rt_result(0, fs_fail_errno_value("ReadDirectory", a[0], "open", EINVAL)));
    DIR *d = opendir(sv_str(a[0]));
    if (!d) return slick_ok(slick_rt_result(0, fs_fail("ReadDirectory", a[0], fs_message_value("open", a[0], errno))));
    char **names = NULL;
    int n = 0;
    struct dirent *ent;
    while ((ent = readdir(d))) {
        if (strcmp(ent->d_name, ".") == 0 || strcmp(ent->d_name, "..") == 0) continue;
        names = realloc(names, sizeof(char *) * (size_t)(n + 1));
        names[n++] = strdup(ent->d_name);
    }
    closedir(d);
    qsort(names, (size_t)n, sizeof(char *), name_cmp);
    slick_value *items = calloc((size_t)n, sizeof(slick_value));
    for (int i = 0; i < n; i++) {
        char path[PATH_MAX];
        snprintf(path, sizeof(path), "%s/%s", sv_str(a[0]), names[i]);
        struct stat st;
        int isdir = stat(path, &st) == 0 && S_ISDIR(st.st_mode);
        items[i] = make_class("std.fs.Entry", "Name", slick_rt_string(names[i], -1), "Path", slick_rt_string(path, -1), "IsDirectory", slick_rt_bool(isdir), NULL);
        free(names[i]);
    }
    free(names);
    return slick_ok(slick_rt_result(1, slick_rt_array(6, n, items)));
}

typedef struct { int closed; char *path; } tmpdir;
slick_outcome slick_nat_fs_tmp(slick_ctx *c, slick_value *a) {
    (void)c;
    slick_value prefix = a[0];
    if (!fs_path_valid(prefix) || memchr(sv_str(prefix), '/', (size_t)sv_len(prefix)) ||
            memchr(sv_str(prefix), '\\', (size_t)sv_len(prefix))) {
        return slick_ok(slick_rt_result(0, fs_fail("CreateTemporaryDirectory", prefix, "pattern contains path separator")));
    }
    char tmpl[PATH_MAX];
    if (snprintf(tmpl, sizeof(tmpl), "/tmp/%.*sXXXXXX", (int)sv_len(prefix), sv_str(prefix)) >= (int)sizeof(tmpl)) {
        return slick_ok(slick_rt_result(0, fs_fail("CreateTemporaryDirectory", prefix, "pattern is too long")));
    }
    if (!mkdtemp(tmpl)) return slick_ok(slick_rt_result(0, fs_fail("CreateTemporaryDirectory", prefix, strerror(errno))));
    tmpdir *t = calloc(1, sizeof(*t));
    t->path = strdup(tmpl);
    slick_value obj = make_class("std.fs.TemporaryDirectory", "Path", slick_rt_string(tmpl, -1), NULL);
    set_resource(obj, t);
    return slick_ok(slick_rt_result(1, obj));
}
static int rm_tree(const char *path) {
    DIR *d = opendir(path);
    if (d) {
        struct dirent *ent;
        while ((ent = readdir(d))) {
            if (strcmp(ent->d_name, ".") == 0 || strcmp(ent->d_name, "..") == 0) continue;
            char child[PATH_MAX];
            snprintf(child, sizeof(child), "%s/%s", path, ent->d_name);
            rm_tree(child);
        }
        closedir(d);
    }
    return remove(path);
}
slick_outcome slick_nat_fs_tmp_close(slick_ctx *c, slick_value *a) {
    (void)c;
    tmpdir *t = get_resource(a[0]);
    slick_value path = class_field(a[0], "Path");
    if (!t) return slick_throw(fs_fail("Close", path, "temporary directory is not owned by this resource"));
    if (t->closed) return slick_ok((slick_value){0, 0, 0});
    if (strcmp(t->path, "/") == 0 || strcmp(t->path, ".") == 0 || t->path[0] == 0) {
        return slick_throw(fs_fail("Close", path, "refusing to remove unsafe cleanup target"));
    }
    if (rm_tree(t->path) != 0) return slick_throw(fs_fail("Close", path, strerror(errno)));
    t->closed = 1;
    return slick_ok((slick_value){0, 0, 0});
}

static char *clean_path(const char *input) {
    if (!input || !*input) return strdup(".");
    int absolute = input[0] == '/';
    char *copy = strdup(input);
    size_t capacity = strlen(input) + 2;
    char **parts = calloc(capacity, sizeof(*parts));
    size_t count = 0;
    char *save = NULL;
    for (char *part = strtok_r(copy, "/", &save); part; part = strtok_r(NULL, "/", &save)) {
        if (!strcmp(part, ".") || !*part) continue;
        if (!strcmp(part, "..")) {
            if (count > 0 && strcmp(parts[count - 1], "..")) {
                count--;
            } else if (!absolute) {
                parts[count++] = part;
            }
        } else {
            parts[count++] = part;
        }
    }
    char *out = calloc(capacity + 1, 1);
    if (absolute) strcat(out, "/");
    for (size_t i = 0; i < count; i++) {
        if ((absolute && strlen(out) > 1) || (!absolute && *out)) strcat(out, "/");
        strcat(out, parts[i]);
    }
    if (!*out) strcpy(out, absolute ? "/" : ".");
    free(parts);
    free(copy);
    return out;
}

slick_outcome slick_nat_path_join(slick_ctx *c, slick_value *a) {
    (void)c;
    int64_t n = slick_rt_array_len(a[0]);
    size_t length = 0;
    for (int64_t i = 0; i < n; i++) length += (size_t)sv_len(slick_rt_array_index(a[0], i)) + 1;
    char *joined = calloc(length + 1, 1);
    for (int64_t i = 0; i < n; i++) {
        slick_value component = slick_rt_array_index(a[0], i);
        if (sv_len(component) == 0) continue;
        if (*joined && joined[strlen(joined) - 1] != '/') strcat(joined, "/");
        strncat(joined, sv_str(component), (size_t)sv_len(component));
    }
    if (!*joined) {
        free(joined);
        return slick_ok(slick_rt_string("", 0));
    }
    char *cleaned = clean_path(joined);
    free(joined);
    slick_value result = slick_rt_string(cleaned, -1);
    free(cleaned);
    return slick_ok(result);
}

slick_outcome slick_nat_path_clean(slick_ctx *c, slick_value *a) {
    (void)c;
    char *cleaned = clean_path(sv_str(a[0]));
    slick_value result = slick_rt_string(cleaned, -1);
    free(cleaned);
    return slick_ok(result);
}

slick_outcome slick_nat_path_base(slick_ctx *c, slick_value *a) {
    (void)c;
    char *cleaned = clean_path(sv_str(a[0]));
    char *slash = strrchr(cleaned, '/');
    const char *base = slash && slash[1] ? slash + 1 : cleaned;
    slick_value result = slick_rt_string(base, -1);
    free(cleaned);
    return slick_ok(result);
}

slick_outcome slick_nat_path_dir(slick_ctx *c, slick_value *a) {
    (void)c;
    char *cleaned = clean_path(sv_str(a[0]));
    char *slash = strrchr(cleaned, '/');
    if (!slash) {
        free(cleaned);
        return slick_ok(slick_rt_string(".", -1));
    }
    if (slash == cleaned) slash[1] = 0;
    else *slash = 0;
    slick_value result = slick_rt_string(cleaned, -1);
    free(cleaned);
    return slick_ok(result);
}

slick_outcome slick_nat_path_ext(slick_ctx *c, slick_value *a) {
    (void)c;
    const char *path = sv_str(a[0]);
    const char *base = strrchr(path, '/');
    base = base ? base + 1 : path;
    const char *dot = strrchr(base, '.');
    if (!dot) return slick_ok(slick_rt_none());
    return slick_ok(slick_rt_some(slick_rt_string(dot, -1)));
}

slick_outcome slick_nat_path_abs(slick_ctx *c, slick_value *a) {
    (void)c;
    return slick_ok(slick_rt_bool(sv_len(a[0]) > 0 && sv_str(a[0])[0] == '/'));
}

static int unicode_space(int32_t value) {
    return (value >= 0x09 && value <= 0x0d) || value == 0x20 || value == 0x85 || value == 0xa0 ||
        value == 0x1680 || (value >= 0x2000 && value <= 0x200a) || value == 0x2028 ||
        value == 0x2029 || value == 0x202f || value == 0x205f || value == 0x3000;
}

slick_outcome slick_nat_text_trim(slick_ctx *c, slick_value *a) {
    (void)c;
    const uint8_t *text = sv_data(a[0]);
    int64_t length = sv_len(a[0]), start = 0, end = 0, offset = 0;
    int leading = 1;
    while (offset < length) {
        int32_t value;
        int width;
        if (!utf8_decode_one(text + offset, length - offset, &value, &width)) {
            value = text[offset];
            width = 1;
        }
        if (unicode_space(value)) {
            if (leading) start = offset + width;
        } else {
            leading = 0;
            end = offset + width;
        }
        offset += width;
    }
    if (leading) start = end = length;
    return slick_ok(slick_rt_string((const char *)text + start, end - start));
}
slick_outcome slick_nat_text_contains(slick_ctx *c, slick_value *a) { (void)c; return slick_ok(slick_rt_bool(strstr(sv_str(a[0]), sv_str(a[1])) != NULL)); }
slick_outcome slick_nat_text_starts(slick_ctx *c, slick_value *a) {
    (void)c;
    return slick_ok(slick_rt_bool(strncmp(sv_str(a[0]), sv_str(a[1]), strlen(sv_str(a[1]))) == 0));
}
slick_outcome slick_nat_text_ends(slick_ctx *c, slick_value *a) {
    (void)c;
    size_t n = strlen(sv_str(a[0])), m = strlen(sv_str(a[1]));
    return slick_ok(slick_rt_bool(n >= m && strcmp(sv_str(a[0]) + n - m, sv_str(a[1])) == 0));
}
slick_outcome slick_nat_text_split(slick_ctx *c, slick_value *a) {
    (void)c;
    const char *s = sv_str(a[0]), *sep = sv_str(a[1]);
    slick_value *items = NULL;
    int n = 0;
    if (!*sep) {
        size_t length = (size_t)sv_len(a[0]);
        size_t offset = 0;
        while (offset < length) {
            unsigned char first = (unsigned char)s[offset];
            size_t width = first < 0x80 ? 1 : first < 0xe0 ? 2 : first < 0xf0 ? 3 : 4;
            if (offset + width > length) width = 1;
            for (size_t i = 1; i < width; i++) {
                if (((unsigned char)s[offset + i] & 0xc0) != 0x80) {
                    width = 1;
                    break;
                }
            }
            items = realloc(items, sizeof(slick_value) * (size_t)(n + 1));
            items[n++] = slick_rt_string(s + offset, (int64_t)width);
            offset += width;
        }
    } else {
        const char *p = s;
        for (;;) {
            const char *f = strstr(p, sep);
            items = realloc(items, sizeof(slick_value) * (size_t)(n + 1));
            if (!f) {
                items[n++] = slick_rt_string(p, -1);
                break;
            }
            items[n++] = slick_rt_string(p, (int64_t)(f - p));
            p = f + strlen(sep);
        }
    }
    return slick_ok(slick_rt_array(6, n, items));
}
slick_outcome slick_nat_text_join(slick_ctx *c, slick_value *a) {
    (void)c;
    int64_t n = slick_rt_array_len(a[0]);
    size_t seplen = strlen(sv_str(a[1])), total = 0;
    for (int64_t i = 0; i < n; i++) {
        total += strlen(sv_str(slick_rt_array_index(a[0], i)));
        if (i + 1 < n) total += seplen;
    }
    char *out = malloc(total + 1);
    out[0] = 0;
    for (int64_t i = 0; i < n; i++) {
        strcat(out, sv_str(slick_rt_array_index(a[0], i)));
        if (i + 1 < n) strcat(out, sv_str(a[1]));
    }
    slick_value v = slick_rt_string(out, (int64_t)total);
    free(out);
    return slick_ok(v);
}
slick_outcome slick_nat_text_replace(slick_ctx *c, slick_value *a) {
    (void)c;
    const char *source = sv_str(a[0]), *old = sv_str(a[1]), *replacement = sv_str(a[2]);
    size_t source_len = (size_t)sv_len(a[0]), old_len = (size_t)sv_len(a[1]), replacement_len = (size_t)sv_len(a[2]);
    if (old_len == 0) {
        size_t runes = 0;
        for (size_t offset = 0; offset < source_len;) {
            int32_t value;
            int width;
            if (!utf8_decode_one((const uint8_t *)source + offset, (int64_t)(source_len - offset), &value, &width)) width = 1;
            offset += (size_t)width;
            runes++;
        }
        size_t output_len = source_len + (runes + 1) * replacement_len;
        char *output = malloc(output_len + 1);
        char *write = output;
        memcpy(write, replacement, replacement_len); write += replacement_len;
        for (size_t offset = 0; offset < source_len;) {
            int32_t value;
            int width;
            if (!utf8_decode_one((const uint8_t *)source + offset, (int64_t)(source_len - offset), &value, &width)) width = 1;
            memcpy(write, source + offset, (size_t)width); write += width; offset += (size_t)width;
            memcpy(write, replacement, replacement_len); write += replacement_len;
        }
        slick_value result = slick_rt_string(output, (int64_t)output_len);
        free(output);
        return slick_ok(result);
    }
    size_t matches = 0;
    for (size_t offset = 0; offset + old_len <= source_len;) {
        if (!memcmp(source + offset, old, old_len)) { matches++; offset += old_len; }
        else offset++;
    }
    size_t output_len = source_len + matches * replacement_len - matches * old_len;
    char *output = malloc(output_len + 1);
    char *write = output;
    for (size_t offset = 0; offset < source_len;) {
        if (offset + old_len <= source_len && !memcmp(source + offset, old, old_len)) {
            memcpy(write, replacement, replacement_len); write += replacement_len; offset += old_len;
        } else {
            *write++ = source[offset++];
        }
    }
    slick_value result = slick_rt_string(output, (int64_t)output_len);
    free(output);
    return slick_ok(result);
}
slick_outcome slick_nat_text_cut(slick_ctx *c, slick_value *a) {
    (void)c;
    const char *s = sv_str(a[0]), *sep = sv_str(a[1]);
    const char *f = strstr(s, sep);
    if (!f) return slick_ok(slick_rt_none());
    slick_value parts[2] = {slick_rt_string(s, (int64_t)(f - s)), slick_rt_string(f + strlen(sep), -1)};
    return slick_ok(slick_rt_some(slick_rt_array(7, 2, parts)));
}
slick_outcome slick_nat_text_quote(slick_ctx *c, slick_value *a) {
    (void)c;
    const uint8_t *source = sv_data(a[0]);
    size_t length = (size_t)sv_len(a[0]);
    static const char hex[] = "0123456789abcdef";
    char *output = malloc(length * 4 + 3);
    char *write = output;
    *write++ = '"';
    for (size_t offset = 0; offset < length;) {
        uint8_t byte = source[offset];
        if (byte == '"' || byte == '\\') {
            *write++ = '\\'; *write++ = (char)byte; offset++; continue;
        }
        const char *escape = NULL;
        switch (byte) {
        case '\a': escape = "a"; break;
        case '\b': escape = "b"; break;
        case '\f': escape = "f"; break;
        case '\n': escape = "n"; break;
        case '\r': escape = "r"; break;
        case '\t': escape = "t"; break;
        case '\v': escape = "v"; break;
        }
        if (escape) {
            *write++ = '\\'; *write++ = *escape; offset++; continue;
        }
        if (byte < 0x20 || byte == 0x7f) {
            *write++ = '\\'; *write++ = 'x'; *write++ = hex[byte >> 4]; *write++ = hex[byte & 15]; offset++; continue;
        }
        if (byte >= 0x80) {
            int32_t value;
            int width;
            if (!utf8_decode_one(source + offset, (int64_t)(length - offset), &value, &width)) {
                *write++ = '\\'; *write++ = 'x'; *write++ = hex[byte >> 4]; *write++ = hex[byte & 15]; offset++; continue;
            }
            memcpy(write, source + offset, (size_t)width);
            write += width;
            offset += (size_t)width;
            continue;
        }
        *write++ = (char)byte;
        offset++;
    }
    *write++ = '"';
    slick_value result = slick_rt_string(output, (int64_t)(write - output));
    free(output);
    return slick_ok(result);
}

typedef struct { int kind; int closed; int64_t pos; uint8_t *data; int64_t len; int64_t cap; } io_res;
slick_outcome slick_nat_io_reader(slick_ctx *c, slick_value *a) {
    (void)c;
    io_res *r = calloc(1, sizeof(*r));
    r->kind = 1;
    r->len = sv_len(a[0]);
    r->data = malloc((size_t)r->len);
    memcpy(r->data, sv_data(a[0]), (size_t)r->len);
    slick_value obj = make_class("std.io.bytesReader", NULL);
    set_resource(obj, r);
    return slick_ok(obj);
}
slick_outcome slick_nat_io_writer(slick_ctx *c, slick_value *a) {
    (void)c; (void)a;
    io_res *r = calloc(1, sizeof(*r));
    r->kind = 2;
    slick_value obj = make_class("std.io.BytesWriter", NULL);
    set_resource(obj, r);
    return slick_ok(obj);
}
static slick_value io_fail(const char *op, const char *msg) {
    return make_class("std.io.Failure", "Operation", slick_rt_string(op, -1), "Message", slick_rt_string(msg, -1), NULL);
}
slick_outcome slick_nat_io_read(slick_ctx *c, slick_value *a) {
    (void)c;
    io_res *r = get_resource(a[0]);
    if (!r || r->closed) return slick_ok(slick_rt_result(0, io_fail("Read", "reader is closed")));
    int64_t max = a[1].bits;
    if (max <= 0) return slick_ok(slick_rt_result(0, io_fail("Read", "MaxBytes must be greater than zero")));
    if (max > 32 * 1024) max = 32 * 1024;
    if (r->pos >= r->len) return slick_ok(slick_rt_result(1, slick_rt_none()));
    int64_t n = r->len - r->pos;
    if (n > max) n = max;
    slick_value chunk = slick_rt_bytes(r->data + r->pos, n);
    r->pos += n;
    return slick_ok(slick_rt_result(1, slick_rt_some(chunk)));
}
slick_outcome slick_nat_io_read_close(slick_ctx *c, slick_value *a) {
    (void)c;
    io_res *r = get_resource(a[0]);
    if (r) r->closed = 1;
    return slick_ok((slick_value){0, 0, 0});
}
slick_outcome slick_nat_io_write(slick_ctx *c, slick_value *a) {
    (void)c;
    io_res *r = get_resource(a[0]);
    if (!r || r->closed) return slick_ok(slick_rt_result(0, io_fail("Write", "writer is closed")));
    int64_t n = sv_len(a[1]);
    if (r->len + n > r->cap) {
        r->cap = (r->len + n) * 2 + 16;
        r->data = realloc(r->data, (size_t)r->cap);
    }
    memcpy(r->data + r->len, sv_data(a[1]), (size_t)n);
    r->len += n;
    return slick_ok(slick_rt_result(1, (slick_value){0, 0, 0}));
}
slick_outcome slick_nat_io_bytes(slick_ctx *c, slick_value *a) {
    (void)c;
    io_res *r = get_resource(a[0]);
    if (!r) return slick_ok(slick_rt_bytes("", 0));
    return slick_ok(slick_rt_bytes(r->data, r->len));
}
slick_outcome slick_nat_io_write_close(slick_ctx *c, slick_value *a) {
    (void)c;
    io_res *r = get_resource(a[0]);
    if (r) r->closed = 1;
    return slick_ok((slick_value){0, 0, 0});
}
static const char *io_failure_message(slick_value failure) {
    slick_value message = class_field(failure, "Message");
    return message.kind == 4 ? sv_str(message) : sv_str(slick_rt_format(failure));
}

static slick_outcome io_read_from(slick_ctx *ctx, slick_value reader, int64_t max_bytes) {
    slick_value argument = slick_rt_int(max_bytes);
    return slick_rt_iface_call(ctx, reader, 1, 1, &argument);
}

static slick_outcome io_write_to(slick_ctx *ctx, slick_value writer, slick_value bytes) {
    return slick_rt_iface_call(ctx, writer, 1, 1, &bytes);
}

slick_outcome slick_nat_io_read_all(slick_ctx *ctx, slick_value *a) {
    int64_t max = a[1].bits;
    if (max < 0) return slick_ok(slick_rt_result(0, io_fail("ReadAll", "MaxBytes must not be negative")));
    uint8_t *bytes = NULL;
    int64_t length = 0;
    for (;;) {
        slick_outcome cancelled_outcome = slick_rt_check_cancel(ctx);
        if (cancelled_outcome.code != SLICK_OK) {
            free(bytes);
            return cancelled_outcome;
        }
        int64_t remaining = max - length;
        int64_t request = remaining < 32 * 1024 ? remaining + 1 : 32 * 1024;
        slick_outcome read = io_read_from(ctx, a[0], request);
        if (read.code != SLICK_OK) {
            free(bytes);
            return read;
        }
        if (!slick_rt_result_ok(read.value)) {
            const char *message = io_failure_message(slick_rt_result_payload(read.value));
            free(bytes);
            return slick_ok(slick_rt_result(0, io_fail("ReadAll", message)));
        }
        slick_value chunk_optional = slick_rt_result_payload(read.value);
        if (!slick_rt_optional_present(chunk_optional)) {
            slick_value result = slick_rt_bytes(bytes, length);
            free(bytes);
            return slick_ok(slick_rt_result(1, result));
        }
        slick_value chunk = slick_rt_optional_value(chunk_optional);
        int64_t chunk_length = sv_len(chunk);
        if (chunk_length == 0) {
            free(bytes);
            return slick_ok(slick_rt_result(0, io_fail("ReadAll", "reader made no progress")));
        }
        if (chunk_length > request) {
            free(bytes);
            return slick_ok(slick_rt_result(0, io_fail("ReadAll", "reader returned a chunk larger than MaxBytes")));
        }
        if (chunk_length > remaining) {
            free(bytes);
            return slick_ok(slick_rt_result(0, io_fail("ReadAll", "byte limit exceeded")));
        }
        bytes = realloc(bytes, (size_t)(length + chunk_length));
        memcpy(bytes + length, sv_data(chunk), (size_t)chunk_length);
        length += chunk_length;
    }
}

slick_outcome slick_nat_io_copy(slick_ctx *ctx, slick_value *a) {
    int64_t max = a[2].bits;
    if (max < 0) return slick_ok(slick_rt_result(0, io_fail("Copy", "MaxBytes must not be negative")));
    int64_t total = 0;
    for (;;) {
        slick_outcome cancelled_outcome = slick_rt_check_cancel(ctx);
        if (cancelled_outcome.code != SLICK_OK) return cancelled_outcome;
        int64_t remaining = max - total;
        int64_t request = remaining < 32 * 1024 ? remaining + 1 : 32 * 1024;
        slick_outcome read = io_read_from(ctx, a[0], request);
        if (read.code != SLICK_OK) return read;
        if (!slick_rt_result_ok(read.value)) {
            return slick_ok(slick_rt_result(0, io_fail("Copy", io_failure_message(slick_rt_result_payload(read.value)))));
        }
        slick_value chunk_optional = slick_rt_result_payload(read.value);
        if (!slick_rt_optional_present(chunk_optional)) return slick_ok(slick_rt_result(1, slick_rt_int(total)));
        slick_value chunk = slick_rt_optional_value(chunk_optional);
        int64_t chunk_length = sv_len(chunk);
        if (chunk_length == 0) return slick_ok(slick_rt_result(0, io_fail("Copy", "reader made no progress")));
        if (chunk_length > request) {
            return slick_ok(slick_rt_result(0, io_fail("Copy", "reader returned a chunk larger than MaxBytes")));
        }
        int64_t write_length = chunk_length > remaining ? remaining : chunk_length;
        if (write_length > 0) {
            slick_value write_chunk = slick_rt_bytes(sv_data(chunk), write_length);
            slick_outcome written = io_write_to(ctx, a[1], write_chunk);
            if (written.code != SLICK_OK) return written;
            if (!slick_rt_result_ok(written.value)) {
                return slick_ok(slick_rt_result(0, io_fail("Copy", io_failure_message(slick_rt_result_payload(written.value)))));
            }
            total += write_length;
        }
        if (chunk_length > remaining) return slick_ok(slick_rt_result(0, io_fail("Copy", "byte limit exceeded")));
    }
}

slick_outcome slick_nat_process_run(slick_ctx *ctx, slick_value *a) {
    if (cancelled(ctx)) {
        return slick_ok(slick_rt_result(0, make_class("std.process.Failure", "Operation", slick_rt_string("Cancelled", -1), "Program", a[0], "Message", slick_rt_string("operation cancelled before child start", -1), NULL)));
    }
    int64_t max = a[3].bits;
    if (max < 0) {
        return slick_ok(slick_rt_result(0, make_class("std.process.Failure", "Operation", slick_rt_string("OutputLimit", -1), "Program", a[0], "Message", slick_rt_string("MaxOutputBytes must not be negative", -1), NULL)));
    }
    int64_t nargs = slick_rt_array_len(a[1]);
    char **argv = calloc((size_t)nargs + 2, sizeof(char *));
    argv[0] = (char *)sv_str(a[0]);
    for (int64_t i = 0; i < nargs; i++) argv[i + 1] = (char *)sv_str(slick_rt_array_index(a[1], i));
    int outp[2], errp[2];
    if (pipe(outp) || pipe(errp)) {
        return slick_ok(slick_rt_result(0, make_class("std.process.Failure", "Operation", slick_rt_string("Spawn", -1), "Program", a[0], "Message", slick_rt_string(strerror(errno), -1), NULL)));
    }
    pid_t pid = fork();
    if (pid < 0) {
        return slick_ok(slick_rt_result(0, make_class("std.process.Failure", "Operation", slick_rt_string("Spawn", -1), "Program", a[0], "Message", slick_rt_string(strerror(errno), -1), NULL)));
    }
    if (pid == 0) {
        if (slick_rt_optional_present(a[2])) {
            if (chdir(sv_str(slick_rt_optional_value(a[2]))) != 0) _exit(127);
        }
        dup2(outp[1], 1);
        dup2(errp[1], 2);
        close(outp[0]); close(outp[1]); close(errp[0]); close(errp[1]);
        execvp(argv[0], argv);
        _exit(127);
    }
    close(outp[1]); close(errp[1]);
    char *obuf = malloc((size_t)max + 1), *ebuf = malloc((size_t)max + 1);
    int64_t olen = 0, elen = 0, total = 0;
    int overflow = 0;
    while (1) {
        if (cancelled(ctx)) {
            kill(pid, SIGTERM);
        }
        struct pollfd fds[2] = {{outp[0], POLLIN, 0}, {errp[0], POLLIN, 0}};
        int pr = poll(fds, 2, 50);
        if (pr < 0 && errno != EINTR) break;
        char tmp[4096];
        if (fds[0].revents & POLLIN) {
            ssize_t n = read(outp[0], tmp, sizeof(tmp));
            if (n > 0) {
                int64_t take = n;
                if (total + take > max) { overflow = 1; take = max - total; kill(pid, SIGKILL); }
                if (take > 0) { memcpy(obuf + olen, tmp, (size_t)take); olen += take; total += take; }
            }
        }
        if (fds[1].revents & POLLIN) {
            ssize_t n = read(errp[0], tmp, sizeof(tmp));
            if (n > 0) {
                int64_t take = n;
                if (total + take > max) { overflow = 1; take = max - total; kill(pid, SIGKILL); }
                if (take > 0) { memcpy(ebuf + elen, tmp, (size_t)take); elen += take; total += take; }
            }
        }
        int status;
        pid_t w = waitpid(pid, &status, WNOHANG);
        if (w == pid) {
            char tmp2[4096];
            ssize_t n;
            while ((n = read(outp[0], tmp2, sizeof(tmp2))) > 0) {
                int64_t take = n;
                if (total + take > max) { overflow = 1; take = max - total; }
                if (take > 0) { memcpy(obuf + olen, tmp2, (size_t)take); olen += take; total += take; }
            }
            while ((n = read(errp[0], tmp2, sizeof(tmp2))) > 0) {
                int64_t take = n;
                if (total + take > max) { overflow = 1; take = max - total; }
                if (take > 0) { memcpy(ebuf + elen, tmp2, (size_t)take); elen += take; total += take; }
            }
            close(outp[0]); close(errp[0]);
            if (cancelled(ctx)) {
                return slick_ok(slick_rt_result(0, make_class("std.process.Failure", "Operation", slick_rt_string("Cancelled", -1), "Program", a[0], "Message", slick_rt_string("operation cancelled; child process was signalled", -1), NULL)));
            }
            if (overflow) {
                char msg[64];
                snprintf(msg, sizeof(msg), "captured output exceeds %" PRId64 " bytes", max);
                return slick_ok(slick_rt_result(0, make_class("std.process.Failure", "Operation", slick_rt_string("OutputLimit", -1), "Program", a[0], "Message", slick_rt_string(msg, -1), NULL)));
            }
            if (WIFSIGNALED(status)) {
                return slick_ok(slick_rt_result(0, make_class("std.process.Failure", "Operation", slick_rt_string("Signal", -1), "Program", a[0], "Message", slick_rt_string("child process was terminated by a signal", -1), NULL)));
            }
            return slick_ok(slick_rt_result(1, make_class("std.process.Completed",
                "ExitCode", slick_rt_int(WEXITSTATUS(status)),
                "Output", slick_rt_bytes(obuf, olen),
                "ErrorOutput", slick_rt_bytes(ebuf, elen), NULL)));
        }
    }
    return slick_ok(slick_rt_result(0, make_class("std.process.Failure", "Operation", slick_rt_string("Wait", -1), "Program", a[0], "Message", slick_rt_string("wait failed", -1), NULL)));
}

static int valid_token_n(const char *s, size_t n) {
    if (n == 0) return 0;
    for (size_t i = 0; i < n; i++) {
        unsigned char c = (unsigned char)s[i];
        if (!(isalnum(c) || strchr("!#$%&'*+-.^_`|~", c))) return 0;
    }
    return 1;
}

static int valid_field_value(const char *s, size_t n) {
    for (size_t i = 0; i < n; i++) {
        unsigned char c = (unsigned char)s[i];
        if (c != '\t' && (c < ' ' || c == 0x7f)) return 0;
    }
    return 1;
}

static void canonical_header(char *out, size_t cap, const char *name, size_t n) {
    int upper = 1;
    size_t take = n < cap - 1 ? n : cap - 1;
    for (size_t i = 0; i < take; i++) {
        unsigned char c = (unsigned char)name[i];
        out[i] = (char)(upper ? toupper(c) : tolower(c));
        upper = c == '-';
    }
    out[take] = 0;
}

static slick_value http_fail(const char *kind, const char *url, const char *msg, slick_value status) {
    return make_class("std.http.Failure", "Kind", slick_rt_string(kind, -1), "URL", slick_rt_string(url ? url : "", -1), "Status", status, "Message", slick_rt_string(msg, -1), NULL);
}

static char *sanitize_url(const char *raw) {
    char *out = strdup(raw ? raw : "");
    char *q = strchr(out, '?');
    char *h = strchr(out, '#');
    char *cut = q && h ? (q < h ? q : h) : (q ? q : h);
    if (cut) *cut = 0;
    char *scheme = strstr(out, "://");
    if (scheme) {
        char *authority = scheme + 3;
        char *slash = strchr(authority, '/');
        char *at = strchr(authority, '@');
        if (at && (!slash || at < slash)) memmove(authority, at + 1, strlen(at + 1) + 1);
    }
    return out;
}

typedef struct native_map_entry {
    slick_value key;
    slick_value value;
} native_map_entry;

typedef struct native_map {
    int64_t len;
    native_map_entry *entries;
} native_map;
typedef struct native_pair {
    slick_value first;
    slick_value second;
} native_pair;

typedef struct native_class {
    int32_t type_id;
    int32_t field_count;
    void *resource;
    slick_value *fields;
} native_class;

typedef struct native_union {
    int32_t type_id;
    int32_t tag;
    int32_t field_count;
    slick_value *fields;
} native_union;

typedef struct native_interface {
    int32_t type_id;
    int32_t method_count;
    slick_value receiver;
    slick_fn *vtable;
} native_interface;

typedef struct native_callable {
    slick_fn code;
    int32_t capture_count;
    int32_t param_count;
    slick_value *captures;
} native_callable;

static int slick_value_task_safe(slick_value value) {
    switch (value.kind) {
    case 0: case 1: case 2: case 3: case 4: case 5:
        return 1;
    case 6: {
        int64_t n = slick_rt_array_len(value);
        for (int64_t i = 0; i < n; i++) if (!slick_value_task_safe(slick_rt_array_index(value, i))) return 0;
        return 1;
    }
    case 7: {
        native_pair *pair = (native_pair *)(uintptr_t)value.bits;
        return pair && slick_value_task_safe(pair->first) && slick_value_task_safe(pair->second);
    }
    case 8: {
        native_map *map = (native_map *)(uintptr_t)value.bits;
        for (int64_t i = 0; map && i < map->len; i++) {
            if (!slick_value_task_safe(map->entries[i].key) || !slick_value_task_safe(map->entries[i].value)) return 0;
        }
        return 1;
    }
    case 9:
        return 0;
    case 10: case 11: {
        native_pair *pair = (native_pair *)(uintptr_t)value.bits;
        return !pair || slick_value_task_safe(pair->first);
    }
    case 12: case 17: {
        native_class *object = (native_class *)(uintptr_t)value.bits;
        if (object && object->type_id == slick_rt_type_id("std.sqlite.Database")) return 1;
        if (!object || object->resource) return 0;
        for (int32_t i = 0; i < object->field_count; i++) if (!slick_value_task_safe(object->fields[i])) return 0;
        return 1;
    }
    case 13: {
        native_union *object = (native_union *)(uintptr_t)value.bits;
        for (int32_t i = 0; object && i < object->field_count; i++) if (!slick_value_task_safe(object->fields[i])) return 0;
        return 1;
    }
    case 14: {
        native_interface *iface = (native_interface *)(uintptr_t)value.bits;
        return iface && slick_value_task_safe(iface->receiver);
    }
    case 15: {
        native_callable *callable = (native_callable *)(uintptr_t)value.bits;
        for (int32_t i = 0; callable && i < callable->capture_count; i++) if (!slick_value_task_safe(callable->captures[i])) return 0;
        return callable != NULL;
    }
    default:
        return 0;
    }
}


#ifdef SLICK_HAS_CURL
typedef struct http_transfer {
    slick_ctx *ctx;
    int64_t max;
    int overflow;
    uint8_t *body;
    size_t length;
    size_t capacity;
    slick_value headers;
    const uint8_t *upload;
    size_t upload_length;
    size_t upload_offset;
} http_transfer;

static pthread_once_t curl_once = PTHREAD_ONCE_INIT;
static CURLSH *curl_share;
static pthread_mutex_t curl_share_mu[CURL_LOCK_DATA_LAST];

static void curl_share_lock(CURL *handle, curl_lock_data data, curl_lock_access access, void *user) {
    (void)handle; (void)access; (void)user;
    pthread_mutex_lock(&curl_share_mu[data]);
}

static void curl_share_unlock(CURL *handle, curl_lock_data data, void *user) {
    (void)handle; (void)user;
    pthread_mutex_unlock(&curl_share_mu[data]);
}

static void init_curl(void) {
    curl_global_init(CURL_GLOBAL_DEFAULT);
    for (int i = 0; i < CURL_LOCK_DATA_LAST; i++) pthread_mutex_init(&curl_share_mu[i], NULL);
    curl_share = curl_share_init();
    curl_share_setopt(curl_share, CURLSHOPT_LOCKFUNC, curl_share_lock);
    curl_share_setopt(curl_share, CURLSHOPT_UNLOCKFUNC, curl_share_unlock);
    curl_share_setopt(curl_share, CURLSHOPT_SHARE, CURL_LOCK_DATA_DNS);
    curl_share_setopt(curl_share, CURLSHOPT_SHARE, CURL_LOCK_DATA_CONNECT);
}

static size_t http_write(char *data, size_t size, size_t count, void *opaque) {
    http_transfer *transfer = opaque;
    size_t n = size * count;
    if (cancelled(transfer->ctx)) return 0;
    if (n > (size_t)INT64_MAX || transfer->length > (size_t)transfer->max ||
        n > (size_t)transfer->max - transfer->length) {
        transfer->overflow = 1;
        return 0;
    }
    if (transfer->length + n > transfer->capacity) {
        size_t capacity = transfer->capacity ? transfer->capacity * 2 : 4096;
        while (capacity < transfer->length + n) capacity *= 2;
        transfer->body = realloc(transfer->body, capacity);
        transfer->capacity = capacity;
    }
    memcpy(transfer->body + transfer->length, data, n);
    transfer->length += n;
    return n;
}

static size_t http_read(char *buffer, size_t size, size_t count, void *opaque) {
    http_transfer *transfer = opaque;
    size_t capacity = size * count;
    size_t remaining = transfer->upload_length - transfer->upload_offset;
    size_t take = remaining < capacity ? remaining : capacity;
    if (take > 0) {
        memcpy(buffer, transfer->upload + transfer->upload_offset, take);
        transfer->upload_offset += take;
    }
    return take;
}

static size_t http_header(char *data, size_t size, size_t count, void *opaque) {
    http_transfer *transfer = opaque;
    size_t n = size * count;
    if (n >= 5 && strncasecmp(data, "HTTP/", 5) == 0) {
        transfer->headers = slick_rt_empty_map();
        return n;
    }
    const char *colon = memchr(data, ':', n);
    if (!colon) return n;
    size_t name_len = (size_t)(colon - data);
    const char *value = colon + 1;
    const char *end = data + n;
    while (value < end && (*value == ' ' || *value == '\t')) value++;
    while (end > value && (end[-1] == '\r' || end[-1] == '\n' || end[-1] == ' ' || end[-1] == '\t')) end--;
    char name[256];
    canonical_header(name, sizeof(name), data, name_len);
    slick_value key = slick_rt_string(name, -1);
    slick_value found = slick_rt_map_get(transfer->headers, key);
    slick_value item = slick_rt_string(value, (int64_t)(end - value));
    slick_value values;
    if (slick_rt_optional_present(found)) {
        slick_value old = slick_rt_optional_value(found);
        int64_t old_len = slick_rt_array_len(old);
        slick_value *items = malloc(sizeof(*items) * (size_t)(old_len + 1));
        for (int64_t i = 0; i < old_len; i++) items[i] = slick_rt_array_index(old, i);
        items[old_len] = item;
        values = slick_rt_array(6, old_len + 1, items);
        free(items);
    } else {
        values = slick_rt_array(6, 1, &item);
    }
    transfer->headers = slick_rt_map_with(transfer->headers, key, values);
    return n;
}

static int http_progress(void *opaque, curl_off_t down_total, curl_off_t down_now, curl_off_t up_total, curl_off_t up_now) {
    (void)down_total; (void)down_now; (void)up_total; (void)up_now;
    return cancelled(((http_transfer *)opaque)->ctx);
}

static int http_url_valid(const char *url, const char **userinfo) {
    const char *authority;
    if (strncasecmp(url, "http://", 7) == 0) authority = url + 7;
    else if (strncasecmp(url, "https://", 8) == 0) authority = url + 8;
    else return 0;
    const char *end = authority + strcspn(authority, "/?#");
    if (end == authority) return 0;
    const char *at = memchr(authority, '@', (size_t)(end - authority));
    if (at) {
        *userinfo = at;
        return 1;
    }
    if (authority[0] == ':' || memchr(authority, ' ', (size_t)(end - authority))) return 0;
    return 1;
}

slick_outcome slick_nat_http_fetch(slick_ctx *ctx, slick_value *a) {
    slick_value req = a[0];
    slick_value method_value = class_field(req, "Method");
    slick_value url_value = class_field(req, "URL");
    const char *method = sv_str(method_value);
    const char *url = sv_str(url_value);
    int64_t method_len = sv_len(method_value);
    int64_t url_len = sv_len(url_value);
    if (method_len < 0 || !valid_token_n(method, (size_t)method_len)) {
        char *safe = sanitize_url(url);
        slick_value failure = http_fail("InvalidRequest", safe, "method must be a non-empty HTTP token", slick_rt_none());
        free(safe);
        return slick_ok(slick_rt_result(0, failure));
    }
    const char *userinfo = NULL;
    if (url_len < 0 || memchr(url, 0, (size_t)url_len) || !http_url_valid(url, &userinfo)) {
        char *safe = sanitize_url(url);
        slick_value failure = http_fail("InvalidRequest", safe, "URL must be an absolute http or https URL", slick_rt_none());
        free(safe);
        return slick_ok(slick_rt_result(0, failure));
    }
    if (userinfo) {
        char *safe = sanitize_url(url);
        slick_value failure = http_fail("InvalidRequest", safe, "URL userinfo is not allowed", slick_rt_none());
        free(safe);
        return slick_ok(slick_rt_result(0, failure));
    }

    int64_t timeout = 30000, max = 8 * 1024 * 1024;
    int follow = 0, body_present = 0;
    slick_value field = class_field(req, "TimeoutMilliseconds");
    if (slick_rt_optional_present(field)) timeout = slick_rt_optional_value(field).bits;
    field = class_field(req, "MaxResponseBytes");
    if (slick_rt_optional_present(field)) max = slick_rt_optional_value(field).bits;
    field = class_field(req, "FollowRedirects");
    if (slick_rt_optional_present(field)) follow = slick_rt_optional_value(field).bits != 0;
    slick_value body_field = class_field(req, "Body");
    slick_value body = {0, 0, 0};
    if (slick_rt_optional_present(body_field)) {
        body_present = 1;
        body = slick_rt_optional_value(body_field);
    }
    if (timeout <= 0 || max <= 0) {
        char *safe = sanitize_url(url);
        const char *message = timeout <= 0 ? "TimeoutMilliseconds must be positive" : "MaxResponseBytes must be positive";
        slick_value failure = http_fail("InvalidRequest", safe, message, slick_rt_none());
        free(safe);
        return slick_ok(slick_rt_result(0, failure));
    }

    struct curl_slist *request_headers = NULL;
    int has_user_agent = 0;
    slick_value headers_field = class_field(req, "Headers");
    if (slick_rt_optional_present(headers_field)) {
        slick_value headers_value = slick_rt_optional_value(headers_field);
        native_map *headers = (native_map *)(uintptr_t)headers_value.bits;
        for (int64_t i = 0; headers && i < headers->len; i++) {
            slick_value name_value = headers->entries[i].key;
            const char *name_raw = sv_str(name_value);
            int64_t name_len = sv_len(name_value);
            char name[256];
            if (name_len < 0 || (size_t)name_len >= sizeof(name) || !valid_token_n(name_raw, (size_t)name_len)) {
                char *safe = sanitize_url(url);
                slick_value failure = http_fail("InvalidRequest", safe, "invalid header name", slick_rt_none());
                free(safe); curl_slist_free_all(request_headers);
                return slick_ok(slick_rt_result(0, failure));
            }
            canonical_header(name, sizeof(name), name_raw, (size_t)name_len);
            if (!strcmp(name, "Host") || !strcmp(name, "Content-Length") || !strcmp(name, "Transfer-Encoding") || !strcmp(name, "Connection")) {
                char message[128];
                snprintf(message, sizeof(message), "%s header cannot be controlled", name);
                char *safe = sanitize_url(url);
                slick_value failure = http_fail("InvalidRequest", safe, message, slick_rt_none());
                free(safe); curl_slist_free_all(request_headers);
                return slick_ok(slick_rt_result(0, failure));
            }
            slick_value values = headers->entries[i].value;
            int64_t values_len = slick_rt_array_len(values);
            if (values_len == 0) {
                char message[128];
                snprintf(message, sizeof(message), "%s header values must not be empty", name);
                char *safe = sanitize_url(url);
                slick_value failure = http_fail("InvalidRequest", safe, message, slick_rt_none());
                free(safe); curl_slist_free_all(request_headers);
                return slick_ok(slick_rt_result(0, failure));
            }
            if (!strcmp(name, "User-Agent")) has_user_agent = 1;
            for (int64_t j = 0; j < values_len; j++) {
                slick_value value = slick_rt_array_index(values, j);
                const char *raw = sv_str(value);
                int64_t length = sv_len(value);
                if (length < 0 || !valid_field_value(raw, (size_t)length)) {
                    char message[160];
                    snprintf(message, sizeof(message), "%s header value contains a forbidden control byte", name);
                    char *safe = sanitize_url(url);
                    slick_value failure = http_fail("InvalidRequest", safe, message, slick_rt_none());
                    free(safe); curl_slist_free_all(request_headers);
                    return slick_ok(slick_rt_result(0, failure));
                }
                size_t line_len = strlen(name) + 2 + (size_t)length;
                char *line = malloc(line_len + 1);
                snprintf(line, line_len + 1, "%s: ", name);
                memcpy(line + strlen(name) + 2, raw, (size_t)length);
                line[line_len] = 0;
                request_headers = curl_slist_append(request_headers, line);
                free(line);
            }
        }
    }
    if (!has_user_agent) request_headers = curl_slist_append(request_headers, "User-Agent: Slick");
    if (body_present) request_headers = curl_slist_append(request_headers, "Transfer-Encoding: chunked");

    pthread_once(&curl_once, init_curl);
    CURL *curl = curl_easy_init();
    http_transfer transfer = {ctx, max, 0, NULL, 0, 0, slick_rt_empty_map(), body_present ? sv_data(body) : NULL, body_present ? (size_t)sv_len(body) : 0, 0};
    curl_easy_setopt(curl, CURLOPT_SHARE, curl_share);
    curl_easy_setopt(curl, CURLOPT_URL, url);
    curl_easy_setopt(curl, CURLOPT_CUSTOMREQUEST, method);
    curl_easy_setopt(curl, CURLOPT_HTTPHEADER, request_headers);
    curl_easy_setopt(curl, CURLOPT_FOLLOWLOCATION, follow ? 1L : 0L);
    curl_easy_setopt(curl, CURLOPT_MAXREDIRS, 10L);
    curl_easy_setopt(curl, CURLOPT_TIMEOUT_MS, timeout > LONG_MAX ? LONG_MAX : (long)timeout);
    curl_easy_setopt(curl, CURLOPT_NOSIGNAL, 1L);
    curl_easy_setopt(curl, CURLOPT_WRITEFUNCTION, http_write);
    curl_easy_setopt(curl, CURLOPT_WRITEDATA, &transfer);
    curl_easy_setopt(curl, CURLOPT_HEADERFUNCTION, http_header);
    curl_easy_setopt(curl, CURLOPT_HEADERDATA, &transfer);
    curl_easy_setopt(curl, CURLOPT_NOPROGRESS, 0L);
    curl_easy_setopt(curl, CURLOPT_XFERINFOFUNCTION, http_progress);
    curl_easy_setopt(curl, CURLOPT_XFERINFODATA, &transfer);
    if (body_present) {
        curl_easy_setopt(curl, CURLOPT_UPLOAD, 1L);
        curl_easy_setopt(curl, CURLOPT_READFUNCTION, http_read);
        curl_easy_setopt(curl, CURLOPT_READDATA, &transfer);
        curl_easy_setopt(curl, CURLOPT_INFILESIZE_LARGE, (curl_off_t)-1);
    }

    CURLcode rc = curl_easy_perform(curl);
    long status_code = 0;
    char *effective = NULL;
    curl_easy_getinfo(curl, CURLINFO_RESPONSE_CODE, &status_code);
    curl_easy_getinfo(curl, CURLINFO_EFFECTIVE_URL, &effective);
    const char *failure_kind = NULL;
    const char *failure_message = NULL;
    if (cancelled(ctx)) {
        failure_kind = "Cancelled"; failure_message = "HTTP request cancelled";
    } else if (rc == CURLE_OPERATION_TIMEDOUT) {
        failure_kind = "Timeout"; failure_message = "HTTP request timed out";
    } else if (transfer.overflow) {
        failure_kind = "BodyTooLarge"; failure_message = "response body exceeds MaxResponseBytes";
    } else if (rc == CURLE_TOO_MANY_REDIRECTS || (follow && rc != CURLE_OK && status_code >= 300 && status_code < 400)) {
        failure_kind = "Redirect"; failure_message = "HTTP redirect failed";
    } else if (rc != CURLE_OK && status_code > 0) {
        failure_kind = "BodyRead"; failure_message = "failed to read response body";
    } else if (rc != CURLE_OK) {
        failure_kind = "Transport"; failure_message = "HTTP transport failed";
    }

    slick_outcome outcome;
    if (failure_kind) {
        char *safe = sanitize_url(effective ? effective : url);
        slick_value status = status_code > 0 ? slick_rt_some(slick_rt_int(status_code)) : slick_rt_none();
        outcome = slick_ok(slick_rt_result(0, http_fail(failure_kind, safe, failure_message, status)));
        free(safe);
    } else {
        slick_value response = make_class("std.http.Response",
            "Status", slick_rt_int(status_code),
            "URL", slick_rt_string(effective ? effective : url, -1),
            "Headers", transfer.headers,
            "Body", slick_rt_bytes(transfer.body, (int64_t)transfer.length), NULL);
        outcome = slick_ok(slick_rt_result(1, response));
    }
    free(transfer.body);
    curl_easy_cleanup(curl);
    curl_slist_free_all(request_headers);
    return outcome;
}
#endif

slick_outcome slick_nat_http_header_values(slick_ctx *c, slick_value *a) {
    (void)c;
    native_map *headers = (native_map *)(uintptr_t)a[0].bits;
    const char *wanted = sv_str(a[1]);
    int64_t wanted_len = sv_len(a[1]);
    for (int64_t i = 0; headers && i < headers->len; i++) {
        slick_value key = headers->entries[i].key;
        if (sv_len(key) == wanted_len && strncasecmp(sv_str(key), wanted, (size_t)wanted_len) == 0) {
            return slick_ok(headers->entries[i].value);
        }
    }
    return slick_ok(slick_rt_array(6, 0, NULL));
}

static const char *status_text(int s) {
    switch (s) {
    case 200: return "OK";
    case 201: return "Created";
    case 204: return "No Content";
    case 301: return "Moved Permanently";
    case 302: return "Found";
    case 400: return "Bad Request";
    case 401: return "Unauthorized";
    case 403: return "Forbidden";
    case 404: return "Not Found";
    case 500: return "Internal Server Error";
    case 502: return "Bad Gateway";
    case 503: return "Service Unavailable";
    default: return NULL;
    }
}
slick_outcome slick_nat_http_status_text(slick_ctx *c, slick_value *a) {
    (void)c;
    const char *t = status_text((int)a[0].bits);
    return slick_ok(t ? slick_rt_some(slick_rt_string(t, -1)) : slick_rt_none());
}

typedef struct http_server {
    slick_ctx *ctx;
    slick_value handler;
    int fd;
    int64_t max_body;
    int64_t max_header;
    int64_t read_timeout_ms;
    int64_t write_timeout_ms;
    int64_t shutdown_timeout_ms;
    volatile int stop;
    pthread_mutex_t workers_mutex;
    int workers;
} http_server;

typedef struct http_handler_job {
    http_server *server;
    slick_ctx ctx;
    slick_value request;
    slick_outcome outcome;
    pthread_mutex_t mutex;
    int done;
} http_handler_job;

static void *http_server_loop(void *arg);
static void *http_connection_main(void *arg);

static slick_value http_server_fail(const char *operation, const char *address, const char *message) {
    return make_class("std.http.server.Failure",
        "Operation", slick_rt_string(operation, -1),
        "Address", slick_rt_string(address ? address : "", -1),
        "Message", slick_rt_string(message, -1), NULL);
}

slick_outcome slick_nat_http_serve(slick_ctx *ctx, slick_value *a) {
    slick_value cfg = a[0];
    slick_value address_value = class_field(cfg, "Address");
    const char *addr = sv_str(address_value);
    if (sv_len(address_value) == 0) {
        return slick_ok(slick_rt_result(0, http_server_fail("Config", "", "Address must not be empty")));
    }
    const char *positive_fields[] = {
        "MaxBodyBytes", "MaxHeaderBytes", "ReadHeaderTimeoutMilliseconds", "ReadTimeoutMilliseconds",
        "WriteTimeoutMilliseconds", "IdleTimeoutMilliseconds", "ShutdownTimeoutMilliseconds",
    };
    for (size_t i = 0; i < sizeof(positive_fields) / sizeof(positive_fields[0]); i++) {
        slick_value value = class_field(cfg, positive_fields[i]);
        if (slick_rt_optional_present(value) && slick_rt_optional_value(value).bits <= 0) {
            char message[128];
            snprintf(message, sizeof(message), "%s must be positive", positive_fields[i]);
            return slick_ok(slick_rt_result(0, http_server_fail("Config", addr, message)));
        }
    }

    char host[256] = {0}, service[16] = {0};
    const char *colon = strrchr(addr, ':');
    if (!slick_value_task_safe(a[1])) {
        return slick_ok(slick_rt_result(0, http_server_fail("Config", addr, "Application must be task-safe")));
    }
    if (!colon || colon == addr || !colon[1] || (size_t)(colon - addr) >= sizeof(host) || strlen(colon + 1) >= sizeof(service)) {
        return slick_ok(slick_rt_result(0, http_server_fail("Bind", addr, "failed to bind listen address")));
    }
    memcpy(host, addr, (size_t)(colon - addr));
    strcpy(service, colon + 1);
    for (const char *p = service; *p; p++) {
        if (!isdigit((unsigned char)*p)) {
            return slick_ok(slick_rt_result(0, http_server_fail("Bind", addr, "failed to bind listen address")));
        }
    }
    long port = strtol(service, NULL, 10);
    if (port < 0 || port > 65535) {
        return slick_ok(slick_rt_result(0, http_server_fail("Bind", addr, "failed to bind listen address")));
    }
    struct addrinfo hints = {0}, *addresses = NULL;
    hints.ai_family = AF_UNSPEC;
    hints.ai_socktype = SOCK_STREAM;
    hints.ai_flags = AI_PASSIVE;
    int lookup = getaddrinfo(host, service, &hints, &addresses);
    if (lookup != 0) {
        return slick_ok(slick_rt_result(0, http_server_fail("Bind", addr, "failed to bind listen address")));
    }
    int fd = -1;
    int bind_error = EADDRNOTAVAIL;
    for (struct addrinfo *candidate = addresses; candidate; candidate = candidate->ai_next) {
        fd = socket(candidate->ai_family, candidate->ai_socktype, candidate->ai_protocol);
        if (fd < 0) {
            bind_error = errno;
            continue;
        }
        int yes = 1;
        setsockopt(fd, SOL_SOCKET, SO_REUSEADDR, &yes, sizeof(yes));
        if (bind(fd, candidate->ai_addr, candidate->ai_addrlen) == 0 && listen(fd, 128) == 0) break;
        bind_error = errno;
        close(fd);
        fd = -1;
    }
    freeaddrinfo(addresses);
    if (fd < 0) {
        return slick_ok(slick_rt_result(0, http_server_fail("Bind", addr, "failed to bind listen address")));
    }
    http_server *srv = calloc(1, sizeof(*srv));
    srv->ctx = ctx;
    srv->handler = a[1];
    srv->fd = fd;
    srv->max_body = 8 << 20;
    srv->max_header = 1 << 20;
    srv->read_timeout_ms = 30000;
    srv->write_timeout_ms = 30000;
    srv->shutdown_timeout_ms = 5000;
    slick_value configured = class_field(cfg, "MaxBodyBytes");
    if (slick_rt_optional_present(configured)) srv->max_body = slick_rt_optional_value(configured).bits;
    configured = class_field(cfg, "MaxHeaderBytes");
    if (slick_rt_optional_present(configured)) srv->max_header = slick_rt_optional_value(configured).bits;
    configured = class_field(cfg, "ReadTimeoutMilliseconds");
    if (slick_rt_optional_present(configured)) srv->read_timeout_ms = slick_rt_optional_value(configured).bits;
    configured = class_field(cfg, "WriteTimeoutMilliseconds");
    if (slick_rt_optional_present(configured)) srv->write_timeout_ms = slick_rt_optional_value(configured).bits;
    configured = class_field(cfg, "ShutdownTimeoutMilliseconds");
    if (slick_rt_optional_present(configured)) srv->shutdown_timeout_ms = slick_rt_optional_value(configured).bits;
    pthread_mutex_init(&srv->workers_mutex, NULL);
    pthread_t accept_thread;
    if (pthread_create(&accept_thread, NULL, http_server_loop, srv) != 0) {
        close(fd);
        return slick_ok(slick_rt_result(0, http_server_fail("Serve", addr, "failed to start HTTP server")));
    }
    while (!cancelled(ctx) && !srv->stop) usleep(10000);
    srv->stop = 1;
    shutdown(fd, SHUT_RDWR);
    close(fd);
    pthread_join(accept_thread, NULL);
    int64_t waited_ms = 0;
    while (waited_ms < srv->shutdown_timeout_ms) {
        pthread_mutex_lock(&srv->workers_mutex);
        int workers = srv->workers;
        pthread_mutex_unlock(&srv->workers_mutex);
        if (workers == 0) break;
        usleep(10000);
        waited_ms += 10;
    }
    return slick_ok(slick_rt_result(1, (slick_value){0, 0, 0}));
}

typedef struct {
    http_server *server;
    int fd;
} http_connection;

typedef struct {
    char *name;
    char *value;
} http_server_header;

static int http_socket_write(int fd, const void *data, size_t length) {
    const uint8_t *bytes = data;
    while (length > 0) {
        ssize_t count = send(fd, bytes, length, MSG_NOSIGNAL);
        if (count <= 0) return 0;
        bytes += count;
        length -= (size_t)count;
    }
    return 1;
}

static int http_socket_printf(int fd, const char *format, ...) {
    va_list args;
    va_start(args, format);
    char *message = NULL;
    int length = vasprintf(&message, format, args);
    va_end(args);
    if (length < 0) return 0;
    int ok = http_socket_write(fd, message, (size_t)length);
    free(message);
    return ok;
}

static void http_simple_response(int fd, int status, const char *reason) {
    http_socket_printf(fd,
        "HTTP/1.1 %d %s\r\nContent-Length: 0\r\nConnection: close\r\n\r\n",
        status, reason);
}

static char *http_trim(char *value) {
    while (*value == ' ' || *value == '\t') value++;
    char *end = value + strlen(value);
    while (end > value && (end[-1] == ' ' || end[-1] == '\t')) *--end = 0;
    return value;
}
static char *http_canonical_header(const char *name, size_t length) {
    char *canonical = malloc(length + 1);
    canonical_header(canonical, length + 1, name, length);
    return canonical;
}

static int http_hex(unsigned char value) {
    if (value >= '0' && value <= '9') return value - '0';
    if (value >= 'a' && value <= 'f') return value - 'a' + 10;
    if (value >= 'A' && value <= 'F') return value - 'A' + 10;
    return -1;
}

static char *http_query_decode(const char *value) {
    size_t length = strlen(value);
    char *decoded = malloc(length + 1);
    char *write = decoded;
    for (size_t i = 0; i < length; i++) {
        unsigned char byte = (unsigned char)value[i];
        if (byte == '+') {
            *write++ = ' ';
        } else if (byte == '%') {
            if (i + 2 >= length) {
                free(decoded);
                return NULL;
            }
            int high = http_hex((unsigned char)value[i + 1]);
            int low = http_hex((unsigned char)value[i + 2]);
            if (high < 0 || low < 0) {
                free(decoded);
                return NULL;
            }
            *write++ = (char)((high << 4) | low);
            i += 2;
        } else {
            *write++ = (char)byte;
        }
    }
    *write = 0;
    return decoded;
}

static slick_value http_map_append(slick_value map, const char *key_text, const char *value_text) {
    slick_value key = slick_rt_string(key_text, -1);
    slick_value found = slick_rt_map_get(map, key);
    slick_value *values = NULL;
    int64_t length = 0;
    if (slick_rt_optional_present(found)) {
        slick_value current = slick_rt_optional_value(found);
        length = slick_rt_array_len(current);
        values = malloc(sizeof(*values) * (size_t)(length + 1));
        for (int64_t i = 0; i < length; i++) values[i] = slick_rt_array_index(current, i);
    } else {
        values = malloc(sizeof(*values));
    }
    values[length] = slick_rt_string(value_text, -1);
    slick_value array = slick_rt_array(6, length + 1, values);
    free(values);
    return slick_rt_map_with(map, key, array);
}

static int http_parse_query(char *raw, slick_value *query) {
    *query = slick_rt_empty_map();
    if (!raw || !*raw) return 1;
    char *part = raw;
    for (;;) {
        char *next = strchr(part, '&');
        if (next) *next = 0;
        char *equals = strchr(part, '=');
        if (equals) *equals = 0;
        char *key = http_query_decode(part);
        char *value = http_query_decode(equals ? equals + 1 : "");
        if (!key || !value) {
            free(key);
            free(value);
            return 0;
        }
        *query = http_map_append(*query, key, value);
        free(key);
        free(value);
        if (!next) return 1;
        part = next + 1;
    }
}

static int http_hop_header(const char *name, char **nominated, size_t nominated_count) {
    static const char *hop[] = {
        "Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate",
        "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade",
    };
    for (size_t i = 0; i < sizeof(hop) / sizeof(hop[0]); i++) {
        if (strcasecmp(name, hop[i]) == 0) return 1;
    }
    for (size_t i = 0; i < nominated_count; i++) {
        if (strcasecmp(name, nominated[i]) == 0) return 1;
    }
    return 0;
}

static void *http_run_handler(void *argument) {
    http_handler_job *job = argument;
    job->outcome = slick_rt_iface_call(&job->ctx, job->server->handler, 0, 1, &job->request);
    pthread_mutex_lock(&job->mutex);
    job->done = 1;
    pthread_mutex_unlock(&job->mutex);
    return NULL;
}

static int http_response_valid(slick_outcome outcome, int *status, slick_value *headers, slick_value *body) {
    if (outcome.code != SLICK_OK) return 0;
    *status = (int)class_field(outcome.value, "Status").bits;
    if (*status < 200 || *status > 599) return 0;
    *headers = class_field(outcome.value, "Headers");
    *body = class_field(outcome.value, "Body");
    return body->kind == 5;
}

static void http_send_handler_response(int fd, const char *method, slick_outcome outcome) {
    int status;
    slick_value headers, body;
    if (!http_response_valid(outcome, &status, &headers, &body)) {
        http_simple_response(fd, 500, "Internal Server Error");
        return;
    }
    native_map *map = NULL;
    if (slick_rt_optional_present(headers)) {
        slick_value value = slick_rt_optional_value(headers);
        map = (native_map *)(uintptr_t)value.bits;
        for (int64_t i = 0; map && i < map->len; i++) {
            const char *name = sv_str(map->entries[i].key);
            if (!valid_token_n(name, (size_t)sv_len(map->entries[i].key)) ||
                http_hop_header(name, NULL, 0)) {
                http_simple_response(fd, 500, "Internal Server Error");
                return;
            }
            slick_value values = map->entries[i].value;
            for (int64_t j = 0; j < slick_rt_array_len(values); j++) {
                slick_value item = slick_rt_array_index(values, j);
                if (!valid_field_value(sv_str(item), (size_t)sv_len(item))) {
                    http_simple_response(fd, 500, "Internal Server Error");
                    return;
                }
            }
        }
    }
    const char *reason = status_text(status);
    if (!reason) reason = "Status";
    int64_t body_length = strcasecmp(method, "HEAD") == 0 || status == 204 ? 0 : sv_len(body);
    if (!http_socket_printf(fd, "HTTP/1.1 %d %s\r\n", status, reason)) return;
    for (int64_t i = 0; map && i < map->len; i++) {
        const char *name = sv_str(map->entries[i].key);
        slick_value values = map->entries[i].value;
        for (int64_t j = 0; j < slick_rt_array_len(values); j++) {
            slick_value item = slick_rt_array_index(values, j);
            if (!http_socket_printf(fd, "%s: %s\r\n", name, sv_str(item))) return;
        }
    }
    if (!http_socket_printf(fd,
        "Content-Length: %" PRId64 "\r\nConnection: close\r\n\r\n", body_length)) return;
    if (body_length > 0) http_socket_write(fd, sv_data(body), (size_t)body_length);
}

static void http_worker_finished(http_server *server) {
    pthread_mutex_lock(&server->workers_mutex);
    server->workers--;
    pthread_mutex_unlock(&server->workers_mutex);
}

static void *http_connection_main(void *argument) {
    http_connection *connection = argument;
    http_server *server = connection->server;
    int fd = connection->fd;
    free(connection);
    struct timeval read_timeout = {
        .tv_sec = server->read_timeout_ms / 1000,
        .tv_usec = (server->read_timeout_ms % 1000) * 1000,
    };
    struct timeval write_timeout = {
        .tv_sec = server->write_timeout_ms / 1000,
        .tv_usec = (server->write_timeout_ms % 1000) * 1000,
    };
    setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &read_timeout, sizeof(read_timeout));
    setsockopt(fd, SOL_SOCKET, SO_SNDTIMEO, &write_timeout, sizeof(write_timeout));

    char *buffer = malloc((size_t)server->max_header + 1);
    size_t used = 0;
    char *header_end = NULL;
    while (used < (size_t)server->max_header) {
        ssize_t count = recv(fd, buffer + used, (size_t)server->max_header - used, 0);
        if (count <= 0) break;
        used += (size_t)count;
        buffer[used] = 0;
        header_end = strstr(buffer, "\r\n\r\n");
        if (header_end) break;
    }
    if (!header_end) {
        http_simple_response(fd, used >= (size_t)server->max_header ? 431 : 400,
            used >= (size_t)server->max_header ? "Request Header Fields Too Large" : "Bad Request");
        free(buffer);
        close(fd);
        http_worker_finished(server);
        return NULL;
    }
    size_t header_bytes = (size_t)(header_end + 4 - buffer);
    char *request_line_end = strstr(buffer, "\r\n");
    if (!request_line_end) {
        http_simple_response(fd, 400, "Bad Request");
        free(buffer);
        close(fd);
        http_worker_finished(server);
        return NULL;
    }
    *request_line_end = 0;
    char method[32], target_text[4096], version[16];
    char extra;
    if (sscanf(buffer, "%31s %4095s %15s %c", method, target_text, version, &extra) != 3 ||
        strncmp(version, "HTTP/1.", 7) != 0) {
        http_simple_response(fd, 400, "Bad Request");
        free(buffer);
        close(fd);
        http_worker_finished(server);
        return NULL;
    }
    if (strcasecmp(method, "CONNECT") == 0) {
        http_simple_response(fd, 500, "Internal Server Error");
        free(buffer);
        close(fd);
        http_worker_finished(server);
        return NULL;
    }
    if (target_text[0] != '/') {
        http_simple_response(fd, 400, "Bad Request");
        free(buffer);
        close(fd);
        http_worker_finished(server);
        return NULL;
    }

    http_server_header *parsed = NULL;
    size_t parsed_count = 0;
    int64_t content_length = 0;
    int content_length_seen = 0;
    char *line = request_line_end + 2;
    while (line < header_end) {
        char *line_end = strstr(line, "\r\n");
        if (!line_end || line_end > header_end) break;
        *line_end = 0;
        char *colon = strchr(line, ':');
        if (!colon) {
            http_simple_response(fd, 400, "Bad Request");
            free(parsed);
            free(buffer);
            close(fd);
            http_worker_finished(server);
            return NULL;
        }
        *colon = 0;
        char *value = http_trim(colon + 1);
        if (!valid_token_n(line, strlen(line)) || !valid_field_value(value, strlen(value))) {
            http_simple_response(fd, 400, "Bad Request");
            free(parsed);
            free(buffer);
            close(fd);
            http_worker_finished(server);
            return NULL;
        }
        char *name = http_canonical_header(line, strlen(line));
        parsed = realloc(parsed, sizeof(*parsed) * (parsed_count + 1));
        parsed[parsed_count++] = (http_server_header){name, value};
        if (strcasecmp(name, "Content-Length") == 0) {
            char *end = NULL;
            errno = 0;
            long long length = strtoll(value, &end, 10);
            if (errno || !end || *end || length < 0 || (content_length_seen && content_length != length)) {
                http_simple_response(fd, 400, "Bad Request");
                for (size_t i = 0; i < parsed_count; i++) free(parsed[i].name);
                free(parsed);
                free(buffer);
                close(fd);
                http_worker_finished(server);
                return NULL;
            }
            content_length = length;
            content_length_seen = 1;
        }
        line = line_end + 2;
    }
    if (content_length > server->max_body) {
        http_simple_response(fd, 413, "Payload Too Large");
        for (size_t i = 0; i < parsed_count; i++) free(parsed[i].name);
        free(parsed);
        free(buffer);
        close(fd);
        http_worker_finished(server);
        return NULL;
    }

    char **nominated = NULL;
    size_t nominated_count = 0;
    for (size_t i = 0; i < parsed_count; i++) {
        if (strcasecmp(parsed[i].name, "Connection") != 0) continue;
        char *copy = strdup(parsed[i].value);
        char *token = copy;
        for (;;) {
            char *comma = strchr(token, ',');
            if (comma) *comma = 0;
            char *trimmed = http_trim(token);
            if (*trimmed) {
                nominated = realloc(nominated, sizeof(*nominated) * (nominated_count + 1));
                nominated[nominated_count++] = http_canonical_header(trimmed, strlen(trimmed));
            }
            if (!comma) break;
            token = comma + 1;
        }
        free(copy);
    }
    slick_value headers = slick_rt_empty_map();
    for (size_t i = 0; i < parsed_count; i++) {
        if (!http_hop_header(parsed[i].name, nominated, nominated_count) &&
            strcasecmp(parsed[i].name, "Content-Length") != 0) {
            headers = http_map_append(headers, parsed[i].name, parsed[i].value);
        }
    }
    for (size_t i = 0; i < nominated_count; i++) free(nominated[i]);
    free(nominated);
    for (size_t i = 0; i < parsed_count; i++) free(parsed[i].name);
    free(parsed);

    uint8_t *body = NULL;
    if (content_length > 0) {
        body = malloc((size_t)content_length);
        size_t available = used > header_bytes ? used - header_bytes : 0;
        if (available > (size_t)content_length) available = (size_t)content_length;
        memcpy(body, buffer + header_bytes, available);
        size_t received = available;
        while (received < (size_t)content_length) {
            ssize_t count = recv(fd, body + received, (size_t)content_length - received, 0);
            if (count <= 0) break;
            received += (size_t)count;
        }
        if (received != (size_t)content_length) {
            http_simple_response(fd, 400, "Bad Request");
            free(body);
            free(buffer);
            close(fd);
            http_worker_finished(server);
            return NULL;
        }
    }
    free(buffer);

    char *target = strdup(target_text);
    char *raw_query = strchr(target, '?');
    if (raw_query) *raw_query++ = 0;
    slick_value query;
    if (!http_parse_query(raw_query, &query)) {
        http_simple_response(fd, 400, "Bad Request");
        free(target);
        free(body);
        close(fd);
        http_worker_finished(server);
        return NULL;
    }
    slick_value request = make_class("std.http.server.Request",
        "Method", slick_rt_string(method, -1),
        "Path", slick_rt_string(target, -1),
        "Query", query,
        "Headers", headers,
        "Body", slick_rt_bytes(body, content_length), NULL);
    free(target);
    free(body);

    http_handler_job job = {
        .server = server,
        .ctx = {0, NULL},
        .request = request,
    };
    pthread_mutex_init(&job.mutex, NULL);
    pthread_t handler_thread;
    if (pthread_create(&handler_thread, NULL, http_run_handler, &job) != 0) {
        http_simple_response(fd, 500, "Internal Server Error");
    } else {
        int client_cancelled = 0;
        for (;;) {
            pthread_mutex_lock(&job.mutex);
            int done = job.done;
            pthread_mutex_unlock(&job.mutex);
            if (done) break;
            if (cancelled(server->ctx) || server->stop) job.ctx.cancelled = 1;
            char probe;
            ssize_t peeked = recv(fd, &probe, 1, MSG_PEEK | MSG_DONTWAIT);
            if (peeked == 0) {
                client_cancelled = 1;
                job.ctx.cancelled = 1;
            }
            usleep(10000);
        }
        pthread_join(handler_thread, NULL);
        if (!client_cancelled && !cancelled(server->ctx) && !server->stop) {
            http_send_handler_response(fd, method, job.outcome);
        }
    }
    pthread_mutex_destroy(&job.mutex);
    shutdown(fd, SHUT_RDWR);
    close(fd);
    http_worker_finished(server);
    return NULL;
}

static void *http_server_loop(void *argument) {
    http_server *server = argument;
    while (!server->stop) {
        struct sockaddr_storage address;
        socklen_t address_length = sizeof(address);
        int fd = accept(server->fd, (struct sockaddr *)&address, &address_length);
        if (fd < 0) {
            if (server->stop) break;
            continue;
        }
        http_connection *connection = malloc(sizeof(*connection));
        connection->server = server;
        connection->fd = fd;
        pthread_mutex_lock(&server->workers_mutex);
        server->workers++;
        pthread_mutex_unlock(&server->workers_mutex);
        pthread_t thread;
        if (pthread_create(&thread, NULL, http_connection_main, connection) != 0) {
            close(fd);
            free(connection);
            http_worker_finished(server);
            continue;
        }
        pthread_detach(thread);
    }
    return NULL;
}

#ifdef SLICK_HAS_SQLITE
typedef struct { sqlite3 *db; int closed; pthread_mutex_t mu; int has_tx; } sdb;
typedef struct { sdb *owner; int closed; int active; } stx;

static slick_value sqlite_fail(const char *op, int *code, const char *msg) {
    slick_value c = code ? slick_rt_some(slick_rt_int(*code)) : slick_rt_none();
    return make_class("std.sqlite.Failure", "Operation", slick_rt_string(op, -1), "Code", c, "Message", slick_rt_string(msg, -1), NULL);
}

static int bind_params(sqlite3_stmt *st, slick_value params) {
    int64_t n = slick_rt_array_len(params);
    for (int64_t i = 0; i < n; i++) {
        slick_value v = slick_rt_array_index(params, i);
        int tag = v.flags;
        if (v.kind == 13) tag = v.flags;
        typedef struct { int32_t type_id; int32_t tag; int32_t field_count; slick_value *fields; } uobj;
        uobj *u = (uobj *)(uintptr_t)v.bits;
        int t = u ? u->tag : 0;
        if (t == 1) sqlite3_bind_null(st, (int)i + 1);
        else if (t == 2) sqlite3_bind_int64(st, (int)i + 1, u->fields[0].bits);
        else if (t == 3) {
            double d; memcpy(&d, &u->fields[0].bits, sizeof(double));
            sqlite3_bind_double(st, (int)i + 1, d);
        } else if (t == 4) sqlite3_bind_text(st, (int)i + 1, sv_str(u->fields[0]), (int)sv_len(u->fields[0]), SQLITE_TRANSIENT);
        else if (t == 5) sqlite3_bind_blob(st, (int)i + 1, sv_data(u->fields[0]), (int)sv_len(u->fields[0]), SQLITE_TRANSIENT);
    }
    return 0;
}

static slick_value sqlite_value(int type, sqlite3_stmt *st, int col) {
    int uid = slick_rt_union_id("std.sqlite.Value");
    if (type == SQLITE_NULL) return slick_rt_union(uid, 1, 0, NULL);
    if (type == SQLITE_INTEGER) {
        slick_value v = slick_rt_int(sqlite3_column_int64(st, col));
        return slick_rt_union(uid, 2, 1, &v);
    }
    if (type == SQLITE_FLOAT) {
        slick_value v = slick_rt_float(sqlite3_column_double(st, col));
        return slick_rt_union(uid, 3, 1, &v);
    }
    if (type == SQLITE_BLOB) {
        const void *p = sqlite3_column_blob(st, col);
        int n = sqlite3_column_bytes(st, col);
        slick_value v = slick_rt_bytes(p, n);
        return slick_rt_union(uid, 5, 1, &v);
    }
    const unsigned char *t = sqlite3_column_text(st, col);
    slick_value v = slick_rt_string((const char *)t, -1);
    return slick_rt_union(uid, 4, 1, &v);
}

slick_outcome slick_nat_sqlite_open(slick_ctx *c, slick_value *a) {
    (void)c;
    sqlite3 *db = NULL;
    int rc = sqlite3_open(sv_str(a[0]), &db);
    if (rc != SQLITE_OK) {
        int code = rc;
        const char *msg = db ? sqlite3_errmsg(db) : "open failed";
        if (db) sqlite3_close(db);
        return slick_ok(slick_rt_result(0, sqlite_fail("Open", &code, msg)));
    }
    sdb *h = calloc(1, sizeof(*h));
    h->db = db;
    pthread_mutex_init(&h->mu, NULL);
    slick_value obj = make_class("std.sqlite.Database", NULL);
    set_resource(obj, h);
    return slick_ok(slick_rt_result(1, obj));
}

static slick_outcome sqlite_exec(slick_ctx *c, sdb *db, slick_value stmt) {
    if (!db || db->closed) return slick_ok(slick_rt_result(0, sqlite_fail("Execute", NULL, "database is closed")));
    if (cancelled(c)) return slick_ok(slick_rt_result(0, sqlite_fail("Execute", NULL, "operation cancelled")));
    sqlite3_stmt *st = NULL;
    int rc = sqlite3_prepare_v2(db->db, sv_str(class_field(stmt, "SQL")), -1, &st, NULL);
    if (rc != SQLITE_OK) {
        int code = rc;
        return slick_ok(slick_rt_result(0, sqlite_fail("Execute", &code, sqlite3_errmsg(db->db))));
    }
    bind_params(st, class_field(stmt, "Parameters"));
    rc = sqlite3_step(st);
    if (rc != SQLITE_DONE && rc != SQLITE_ROW) {
        int code = rc;
        sqlite3_finalize(st);
        return slick_ok(slick_rt_result(0, sqlite_fail("Execute", &code, sqlite3_errmsg(db->db))));
    }
    int64_t changes = sqlite3_changes64(db->db);
    sqlite3_int64 last = sqlite3_last_insert_rowid(db->db);
    sqlite3_finalize(st);
    return slick_ok(slick_rt_result(1, make_class("std.sqlite.Execution",
        "RowsAffected", slick_rt_int(changes),
        "LastInsertId", slick_rt_some(slick_rt_int(last)), NULL)));
}

static slick_outcome sqlite_query(slick_ctx *c, sdb *db, slick_value q) {
    if (!db || db->closed) return slick_ok(slick_rt_result(0, sqlite_fail("Query", NULL, "database is closed")));
    if (cancelled(c)) return slick_ok(slick_rt_result(0, sqlite_fail("Query", NULL, "operation cancelled")));
    sqlite3_stmt *st = NULL;
    int rc = sqlite3_prepare_v2(db->db, sv_str(class_field(q, "SQL")), -1, &st, NULL);
    if (rc != SQLITE_OK) {
        int code = rc;
        return slick_ok(slick_rt_result(0, sqlite_fail("Query", &code, sqlite3_errmsg(db->db))));
    }
    bind_params(st, class_field(q, "Parameters"));
    int64_t max_rows = class_field(q, "MaxRows").bits;
    slick_value *rows = NULL;
    int64_t n = 0;
    while ((rc = sqlite3_step(st)) == SQLITE_ROW) {
        if (n >= max_rows) break;
        int cols = sqlite3_column_count(st);
        slick_value map = slick_rt_empty_map();
        for (int i = 0; i < cols; i++) {
            map = slick_rt_map_with(map, slick_rt_string(sqlite3_column_name(st, i), -1),
                sqlite_value(sqlite3_column_type(st, i), st, i));
        }
        rows = realloc(rows, sizeof(slick_value) * (size_t)(n + 1));
        rows[n++] = make_class("std.sqlite.Row", "Values", map, NULL);
    }
    sqlite3_finalize(st);
    return slick_ok(slick_rt_result(1, slick_rt_array(6, n, rows)));
}

slick_outcome slick_nat_sqlite_db_exec(slick_ctx *c, slick_value *a) { return sqlite_exec(c, get_resource(a[0]), a[1]); }
slick_outcome slick_nat_sqlite_db_query(slick_ctx *c, slick_value *a) { return sqlite_query(c, get_resource(a[0]), a[1]); }
slick_outcome slick_nat_sqlite_db_begin(slick_ctx *c, slick_value *a) {
    (void)c;
    sdb *db = get_resource(a[0]);
    if (!db || db->closed) return slick_ok(slick_rt_result(0, sqlite_fail("Begin", NULL, "database is closed")));
    char *err = NULL;
    if (sqlite3_exec(db->db, "BEGIN", NULL, NULL, &err) != SQLITE_OK) {
        slick_value f = sqlite_fail("Begin", NULL, err ? err : "begin failed");
        sqlite3_free(err);
        return slick_ok(slick_rt_result(0, f));
    }
    stx *tx = calloc(1, sizeof(*tx));
    tx->owner = db;
    tx->active = 1;
    db->has_tx = 1;
    slick_value obj = make_class("std.sqlite.Transaction", NULL);
    set_resource(obj, tx);
    return slick_ok(slick_rt_result(1, obj));
}
slick_outcome slick_nat_sqlite_db_close(slick_ctx *c, slick_value *a) {
    (void)c;
    sdb *db = get_resource(a[0]);
    if (!db || db->closed) return slick_ok((slick_value){0, 0, 0});
    db->closed = 1;
    sqlite3_close(db->db);
    return slick_ok((slick_value){0, 0, 0});
}
slick_outcome slick_nat_sqlite_tx_exec(slick_ctx *c, slick_value *a) {
    stx *tx = get_resource(a[0]);
    if (!tx || !tx->active) return slick_ok(slick_rt_result(0, sqlite_fail("Execute", NULL, "transaction is closed")));
    return sqlite_exec(c, tx->owner, a[1]);
}
slick_outcome slick_nat_sqlite_tx_query(slick_ctx *c, slick_value *a) {
    stx *tx = get_resource(a[0]);
    if (!tx || !tx->active) return slick_ok(slick_rt_result(0, sqlite_fail("Query", NULL, "transaction is closed")));
    return sqlite_query(c, tx->owner, a[1]);
}
static slick_outcome sqlite_tx_end(stx *tx, const char *sql, const char *op) {
    if (!tx || !tx->active) return slick_ok(slick_rt_result(0, sqlite_fail(op, NULL, "transaction is closed")));
    char *err = NULL;
    int rc = sqlite3_exec(tx->owner->db, sql, NULL, NULL, &err);
    tx->active = 0;
    tx->owner->has_tx = 0;
    if (rc != SQLITE_OK) {
        slick_value f = sqlite_fail(op, NULL, err ? err : op);
        sqlite3_free(err);
        return slick_ok(slick_rt_result(0, f));
    }
    return slick_ok(slick_rt_result(1, (slick_value){0, 0, 0}));
}
slick_outcome slick_nat_sqlite_tx_commit(slick_ctx *c, slick_value *a) { (void)c; return sqlite_tx_end(get_resource(a[0]), "COMMIT", "Commit"); }
slick_outcome slick_nat_sqlite_tx_rollback(slick_ctx *c, slick_value *a) { (void)c; return sqlite_tx_end(get_resource(a[0]), "ROLLBACK", "Rollback"); }
slick_outcome slick_nat_sqlite_tx_close(slick_ctx *c, slick_value *a) {
    (void)c;
    stx *tx = get_resource(a[0]);
    if (!tx || tx->closed) return slick_ok((slick_value){0, 0, 0});
    if (tx->active) sqlite_tx_end(tx, "ROLLBACK", "Close");
    tx->closed = 1;
    return slick_ok((slick_value){0, 0, 0});
}
#else
slick_outcome slick_nat_sqlite_open(slick_ctx *c, slick_value *a) { (void)c; (void)a; return slick_throw(slick_rt_string("LLVM backend requires libsqlite3", -1)); }
slick_outcome slick_nat_sqlite_db_exec(slick_ctx *c, slick_value *a) { (void)c; (void)a; return slick_throw(slick_rt_string("LLVM backend requires libsqlite3", -1)); }
slick_outcome slick_nat_sqlite_db_query(slick_ctx *c, slick_value *a) { (void)c; (void)a; return slick_throw(slick_rt_string("LLVM backend requires libsqlite3", -1)); }
slick_outcome slick_nat_sqlite_db_begin(slick_ctx *c, slick_value *a) { (void)c; (void)a; return slick_throw(slick_rt_string("LLVM backend requires libsqlite3", -1)); }
slick_outcome slick_nat_sqlite_db_close(slick_ctx *c, slick_value *a) { (void)c; (void)a; return slick_ok((slick_value){0,0,0}); }
slick_outcome slick_nat_sqlite_tx_exec(slick_ctx *c, slick_value *a) { (void)c; (void)a; return slick_throw(slick_rt_string("LLVM backend requires libsqlite3", -1)); }
slick_outcome slick_nat_sqlite_tx_query(slick_ctx *c, slick_value *a) { (void)c; (void)a; return slick_throw(slick_rt_string("LLVM backend requires libsqlite3", -1)); }
slick_outcome slick_nat_sqlite_tx_commit(slick_ctx *c, slick_value *a) { (void)c; (void)a; return slick_throw(slick_rt_string("LLVM backend requires libsqlite3", -1)); }
slick_outcome slick_nat_sqlite_tx_rollback(slick_ctx *c, slick_value *a) { (void)c; (void)a; return slick_throw(slick_rt_string("LLVM backend requires libsqlite3", -1)); }
slick_outcome slick_nat_sqlite_tx_close(slick_ctx *c, slick_value *a) { (void)c; (void)a; return slick_ok((slick_value){0,0,0}); }
#endif
