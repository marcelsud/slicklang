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
typedef struct slick_ctx { volatile int cancelled; void *scope; struct slick_ctx *parent; } slick_ctx;
typedef slick_outcome (*slick_fn)(void *ctx, slick_value *args);

enum { SLICK_OK = 0, SLICK_THROW = 1, SLICK_CANCEL = 5 };

slick_value slick_rt_bool(int32_t v);
slick_value slick_rt_int(int64_t v);
slick_value slick_rt_float(double v);
slick_value slick_rt_float_text(double v);
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
void *slick_rt_arena_push(void);
void slick_rt_arena_pop(void *arena);
void *slick_rt_arena_current(void);
void *slick_rt_arena_enter(void *arena);
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
    for (; ctx; ctx = ctx->parent) {
        if (__atomic_load_n(&ctx->cancelled, __ATOMIC_ACQUIRE)) return 1;
    }
    return 0;
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
    return slick_ok(slick_rt_float_text(v));
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
    size_t message_length = strlen(verb) + 1 + (size_t)sv_len(path) + 2 + strlen(strerror(code));
    char *message = fs_message_n(verb, sv_str(path), (size_t)sv_len(path), code);
    slick_value failure = make_class("std.fs.Failure", "Operation", slick_rt_string(op, -1), "Path", path,
        "Message", slick_rt_string(message, (int64_t)message_length), NULL);
    free(message);
    return failure;
}

slick_outcome slick_nat_fs_read_text(slick_ctx *ctx, slick_value *a) {
    if (cancelled(ctx)) return slick_ok(slick_rt_result(0, fs_fail("ReadText", a[0], "operation cancelled")));
    if (!fs_path_valid(a[0])) return slick_ok(slick_rt_result(0, fs_fail_errno_value("ReadText", a[0], "open", EINVAL)));
    int fd = open(sv_str(a[0]), O_RDONLY | O_NONBLOCK);
    if (fd < 0) return slick_ok(slick_rt_result(0, fs_fail_errno_value("ReadText", a[0], "open", errno)));
    struct stat file_status;
    int is_fifo = fstat(fd, &file_status) == 0 && S_ISFIFO(file_status.st_mode);
    int fifo_connected = 0;
    uint8_t *buffer = NULL;
    size_t length = 0, capacity = 0;
    for (;;) {
        if (cancelled(ctx)) {
            close(fd);
            free(buffer);
            return slick_ok(slick_rt_result(0, fs_fail("ReadText", a[0], "operation cancelled")));
        }
        if (length + 32768 > capacity) {
            size_t grown_capacity = capacity ? capacity * 2 : 32768;
            uint8_t *grown = realloc(buffer, grown_capacity);
            if (!grown) {
                close(fd);
                free(buffer);
                return slick_ok(slick_rt_result(0, fs_fail("ReadText", a[0], "out of memory")));
            }
            buffer = grown;
            capacity = grown_capacity;
        }
        ssize_t count = read(fd, buffer + length, capacity - length);
        if (count > 0) {
            if (is_fifo) fifo_connected = 1;
            length += (size_t)count;
            continue;
        }
        if (count == 0) {
            if (is_fifo && !fifo_connected) {
                struct pollfd wait_for_writer = {.fd = fd, .events = POLLIN | POLLHUP};
                int ready = poll(&wait_for_writer, 1, 10);
                if (ready < 0 && errno == EINTR) continue;
                if (ready > 0 && (wait_for_writer.revents & POLLIN)) continue;
                if (ready > 0 && (wait_for_writer.revents & POLLHUP)) break;
                if (ready < 0) {
                    int code = errno;
                    close(fd);
                    free(buffer);
                    return slick_ok(slick_rt_result(0, fs_fail_errno_value("ReadText", a[0], "read", code)));
                }
                continue;
            }
            break;
        }
        if (errno == EINTR) continue;
        if (errno == EAGAIN || errno == EWOULDBLOCK) {
            if (is_fifo) fifo_connected = 1;
            usleep(10000);
            continue;
        }
        int code = errno;
        close(fd);
        free(buffer);
        return slick_ok(slick_rt_result(0, fs_fail_errno_value("ReadText", a[0], "read", code)));
    }
    if (close(fd) != 0) {
        int code = errno;
        free(buffer);
        return slick_ok(slick_rt_result(0, fs_fail_errno_value("ReadText", a[0], "close", code)));
    }
    if (!utf8_valid(buffer, (int64_t)length).bits) {
        free(buffer);
        return slick_ok(slick_rt_result(0, fs_fail("ReadText", a[0], "invalid UTF-8")));
    }
    slick_value output = slick_rt_string((const char *)buffer, (int64_t)length);
    free(buffer);
    return slick_ok(slick_rt_result(1, output));
}

slick_outcome slick_nat_fs_write_text(slick_ctx *ctx, slick_value *a) {
    if (cancelled(ctx)) return slick_ok(slick_rt_result(0, fs_fail("WriteText", a[0], "operation cancelled")));
    if (!fs_path_valid(a[0])) return slick_ok(slick_rt_result(0, fs_fail_errno_value("WriteText", a[0], "open", EINVAL)));
    int fd = open(sv_str(a[0]), O_WRONLY | O_CREAT | O_TRUNC | O_NONBLOCK, 0666);
    if (fd < 0) return slick_ok(slick_rt_result(0, fs_fail_errno_value("WriteText", a[0], "open", errno)));
    const uint8_t *text = sv_data(a[1]);
    size_t length = (size_t)sv_len(a[1]), offset = 0;
    while (offset < length) {
        if (cancelled(ctx)) {
            close(fd);
            return slick_ok(slick_rt_result(0, fs_fail("WriteText", a[0], "operation cancelled")));
        }
        ssize_t count = write(fd, text + offset, length - offset);
        if (count > 0) {
            offset += (size_t)count;
            continue;
        }
        if (count < 0 && errno == EINTR) continue;
        if (count < 0 && (errno == EAGAIN || errno == EWOULDBLOCK)) {
            usleep(10000);
            continue;
        }
        int code = count < 0 ? errno : EIO;
        close(fd);
        return slick_ok(slick_rt_result(0, fs_fail_errno_value("WriteText", a[0], "write", code)));
    }
    if (close(fd) != 0) return slick_ok(slick_rt_result(0, fs_fail_errno_value("WriteText", a[0], "close", errno)));
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
static int64_t bytes_find(const uint8_t *source, size_t source_len,
    const uint8_t *needle, size_t needle_len) {
    if (needle_len == 0) return 0;
    if (needle_len > source_len) return -1;
    for (size_t offset = 0; offset + needle_len <= source_len; offset++) {
        if (memcmp(source + offset, needle, needle_len) == 0) return (int64_t)offset;
    }
    return -1;
}

slick_outcome slick_nat_text_contains(slick_ctx *c, slick_value *a) {
    (void)c;
    return slick_ok(slick_rt_bool(bytes_find(sv_data(a[0]), (size_t)sv_len(a[0]),
        sv_data(a[1]), (size_t)sv_len(a[1])) >= 0));
}
slick_outcome slick_nat_text_starts(slick_ctx *c, slick_value *a) {
    (void)c;
    size_t source_len = (size_t)sv_len(a[0]), prefix_len = (size_t)sv_len(a[1]);
    return slick_ok(slick_rt_bool(prefix_len == 0 || (source_len >= prefix_len &&
        memcmp(sv_data(a[0]), sv_data(a[1]), prefix_len) == 0)));
}
slick_outcome slick_nat_text_ends(slick_ctx *c, slick_value *a) {
    (void)c;
    size_t source_len = (size_t)sv_len(a[0]), suffix_len = (size_t)sv_len(a[1]);
    return slick_ok(slick_rt_bool(suffix_len == 0 || (source_len >= suffix_len &&
        memcmp(sv_data(a[0]) + source_len - suffix_len, sv_data(a[1]), suffix_len) == 0)));
}
slick_outcome slick_nat_text_split(slick_ctx *c, slick_value *a) {
    (void)c;
    const uint8_t *source = sv_data(a[0]), *separator = sv_data(a[1]);
    size_t source_len = (size_t)sv_len(a[0]), separator_len = (size_t)sv_len(a[1]);
    slick_value *items = NULL;
    int n = 0;
    if (separator_len == 0) {
        size_t offset = 0;
        while (offset < source_len) {
            int32_t value;
            int width;
            if (!utf8_decode_one(source + offset, (int64_t)(source_len - offset), &value, &width)) width = 1;
            items = realloc(items, sizeof(*items) * (size_t)(n + 1));
            items[n++] = slick_rt_string((const char *)source + offset, width);
            offset += (size_t)width;
        }
    } else {
        size_t offset = 0;
        for (;;) {
            int64_t relative = bytes_find(source + offset, source_len - offset, separator, separator_len);
            items = realloc(items, sizeof(*items) * (size_t)(n + 1));
            if (relative < 0) {
                items[n++] = slick_rt_string((const char *)source + offset, (int64_t)(source_len - offset));
                break;
            }
            size_t found_offset = offset + (size_t)relative;
            items[n++] = slick_rt_string((const char *)source + offset, (int64_t)(found_offset - offset));
            offset = found_offset + separator_len;
        }
    }
    slick_value result = slick_rt_array(6, n, items);
    free(items);
    return slick_ok(result);
}
slick_outcome slick_nat_text_join(slick_ctx *c, slick_value *a) {
    (void)c;
    int64_t n = slick_rt_array_len(a[0]);
    size_t separator_len = (size_t)sv_len(a[1]), total = 0;
    for (int64_t i = 0; i < n; i++) {
        total += (size_t)sv_len(slick_rt_array_index(a[0], i));
        if (i + 1 < n) total += separator_len;
    }
    char *output = malloc(total + 1);
    char *write = output;
    for (int64_t i = 0; i < n; i++) {
        slick_value item = slick_rt_array_index(a[0], i);
        if (sv_len(item) > 0) {
            memcpy(write, sv_data(item), (size_t)sv_len(item));
            write += sv_len(item);
        }
        if (i + 1 < n && separator_len > 0) {
            memcpy(write, sv_data(a[1]), separator_len);
            write += separator_len;
        }
    }
    *write = 0;
    slick_value value = slick_rt_string(output, (int64_t)total);
    free(output);
    return slick_ok(value);
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
    const uint8_t *source = sv_data(a[0]), *separator = sv_data(a[1]);
    size_t source_len = (size_t)sv_len(a[0]), separator_len = (size_t)sv_len(a[1]);
    int64_t found = bytes_find(source, source_len, separator, separator_len);
    if (found < 0) return slick_ok(slick_rt_none());
    size_t offset = (size_t)found;
    slick_value parts[2] = {
        slick_rt_string((const char *)source, (int64_t)offset),
        slick_rt_string((const char *)source + offset + separator_len, (int64_t)(source_len - offset - separator_len)),
    };
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

static int64_t monotonic_milliseconds(void) {
    struct timespec now;
    clock_gettime(CLOCK_MONOTONIC, &now);
    return (int64_t)now.tv_sec * 1000 + now.tv_nsec / 1000000;
}

static void process_capture(int fd, char *buffer, int64_t *length, int64_t *total,
    int64_t maximum, int *open_pipe, int *overflow) {
    char chunk[4096];
    for (;;) {
        ssize_t count = read(fd, chunk, sizeof(chunk));
        if (count > 0) {
            int64_t take = count;
            if (take > maximum - *total) {
                *overflow = 1;
                take = maximum - *total;
            }
            if (take > 0) {
                memcpy(buffer + *length, chunk, (size_t)take);
                *length += take;
                *total += take;
            }
            continue;
        }
        if (count == 0) *open_pipe = 0;
        if (count < 0 && errno == EINTR) continue;
        return;
    }
}

slick_outcome slick_nat_process_run(slick_ctx *ctx, slick_value *a) {
    if (cancelled(ctx)) {
        return slick_ok(slick_rt_result(0, make_class("std.process.Failure", "Operation", slick_rt_string("Cancelled", -1), "Program", a[0], "Message", slick_rt_string("operation cancelled before child start", -1), NULL)));
    }
    int64_t maximum = a[3].bits;
    if (maximum < 0 || (uint64_t)maximum > SIZE_MAX - 1) {
        return slick_ok(slick_rt_result(0, make_class("std.process.Failure", "Operation", slick_rt_string("OutputLimit", -1), "Program", a[0], "Message", slick_rt_string("MaxOutputBytes must not be negative or exceed addressable memory", -1), NULL)));
    }
    if (memchr(sv_data(a[0]), 0, (size_t)sv_len(a[0]))) {
        return slick_ok(slick_rt_result(0, make_class("std.process.Failure", "Operation", slick_rt_string("Spawn", -1), "Program", a[0], "Message", slick_rt_string("program contains NUL", -1), NULL)));
    }
    if (slick_rt_optional_present(a[2])) {
        slick_value directory = slick_rt_optional_value(a[2]);
        struct stat info;
        if (memchr(sv_data(directory), 0, (size_t)sv_len(directory)) ||
            stat(sv_str(directory), &info) != 0 || !S_ISDIR(info.st_mode)) {
            return slick_ok(slick_rt_result(0, make_class("std.process.Failure", "Operation", slick_rt_string("WorkingDirectory", -1), "Program", a[0], "Message", slick_rt_string("working directory is not an existing directory", -1), NULL)));
        }
    }
    int64_t argument_count = slick_rt_array_len(a[1]);
    char **argv = calloc((size_t)argument_count + 2, sizeof(*argv));
    if (!argv) {
        return slick_ok(slick_rt_result(0, make_class("std.process.Failure", "Operation", slick_rt_string("Spawn", -1), "Program", a[0], "Message", slick_rt_string("out of memory", -1), NULL)));
    }
    argv[0] = (char *)sv_str(a[0]);
    for (int64_t i = 0; i < argument_count; i++) {
        slick_value argument = slick_rt_array_index(a[1], i);
        if (memchr(sv_data(argument), 0, (size_t)sv_len(argument))) {
            free(argv);
            return slick_ok(slick_rt_result(0, make_class("std.process.Failure", "Operation", slick_rt_string("Spawn", -1), "Program", a[0], "Message", slick_rt_string("argument contains NUL", -1), NULL)));
        }
        argv[i + 1] = (char *)sv_str(argument);
    }
    int output_pipe[2] = {-1, -1}, error_pipe[2] = {-1, -1}, setup_pipe[2] = {-1, -1};
    if (pipe(output_pipe) != 0 || pipe(error_pipe) != 0 || pipe(setup_pipe) != 0) {
        int code = errno;
        if (output_pipe[0] >= 0) close(output_pipe[0]);
        if (output_pipe[1] >= 0) close(output_pipe[1]);
        if (error_pipe[0] >= 0) close(error_pipe[0]);
        if (error_pipe[1] >= 0) close(error_pipe[1]);
        if (setup_pipe[0] >= 0) close(setup_pipe[0]);
        if (setup_pipe[1] >= 0) close(setup_pipe[1]);
        free(argv);
        return slick_ok(slick_rt_result(0, make_class("std.process.Failure", "Operation", slick_rt_string("Spawn", -1), "Program", a[0], "Message", slick_rt_string(strerror(code), -1), NULL)));
    }
    pid_t pid = fork();
    if (pid < 0) {
        int code = errno;
        close(output_pipe[0]); close(output_pipe[1]); close(error_pipe[0]); close(error_pipe[1]);
        close(setup_pipe[0]); close(setup_pipe[1]);
        free(argv);
        return slick_ok(slick_rt_result(0, make_class("std.process.Failure", "Operation", slick_rt_string("Spawn", -1), "Program", a[0], "Message", slick_rt_string(strerror(code), -1), NULL)));
    }
    if (pid == 0) {
        close(setup_pipe[0]);
        fcntl(setup_pipe[1], F_SETFD, FD_CLOEXEC);
        setpgid(0, 0);
        int setup_error = 0;
        if (slick_rt_optional_present(a[2]) && chdir(sv_str(slick_rt_optional_value(a[2]))) != 0) setup_error = errno;
        if (!setup_error && dup2(output_pipe[1], STDOUT_FILENO) < 0) setup_error = errno;
        if (!setup_error && dup2(error_pipe[1], STDERR_FILENO) < 0) setup_error = errno;
        if (setup_error) {
            ssize_t reported;
            do {
                reported = write(setup_pipe[1], &setup_error, sizeof(setup_error));
            } while (reported < 0 && errno == EINTR);
            _exit(127);
        }
        close(output_pipe[0]); close(output_pipe[1]); close(error_pipe[0]); close(error_pipe[1]);
        execvp(argv[0], argv);
        setup_error = errno;
        ssize_t reported;
        do {
            reported = write(setup_pipe[1], &setup_error, sizeof(setup_error));
        } while (reported < 0 && errno == EINTR);
        _exit(127);
    }
    free(argv);
    setpgid(pid, pid);
    close(output_pipe[1]);
    close(error_pipe[1]);
    close(setup_pipe[1]);
    int setup_error = 0;
    ssize_t setup_count;
    do {
        setup_count = read(setup_pipe[0], &setup_error, sizeof(setup_error));
    } while (setup_count < 0 && errno == EINTR);
    close(setup_pipe[0]);
    if (setup_count > 0) {
        waitpid(pid, NULL, 0);
        close(output_pipe[0]);
        close(error_pipe[0]);
        return slick_ok(slick_rt_result(0, make_class("std.process.Failure", "Operation", slick_rt_string("Spawn", -1), "Program", a[0], "Message", slick_rt_string(strerror(setup_error), -1), NULL)));
    }
    fcntl(output_pipe[0], F_SETFL, fcntl(output_pipe[0], F_GETFL, 0) | O_NONBLOCK);
    fcntl(error_pipe[0], F_SETFL, fcntl(error_pipe[0], F_GETFL, 0) | O_NONBLOCK);
    char *output = malloc((size_t)maximum + 1), *error_output = malloc((size_t)maximum + 1);
    if (!output || !error_output) {
        kill(-pid, SIGKILL);
        waitpid(pid, NULL, 0);
        close(output_pipe[0]); close(error_pipe[0]);
        free(output); free(error_output);
        return slick_ok(slick_rt_result(0, make_class("std.process.Failure", "Operation", slick_rt_string("Spawn", -1), "Program", a[0], "Message", slick_rt_string("out of memory", -1), NULL)));
    }
    int64_t output_length = 0, error_length = 0, total = 0, cancelled_at = 0;
    int overflow = 0, output_open = 1, error_open = 1, status = 0, wait_failure = 0;
    for (;;) {
        if (cancelled(ctx) && cancelled_at == 0) {
            cancelled_at = monotonic_milliseconds();
            kill(-pid, SIGTERM);
        }
        if (cancelled_at && monotonic_milliseconds() - cancelled_at >= 250) kill(-pid, SIGKILL);
        if (overflow) kill(-pid, SIGKILL);
        struct pollfd descriptors[2] = {
            {output_pipe[0], POLLIN | POLLHUP, 0},
            {error_pipe[0], POLLIN | POLLHUP, 0},
        };
        int poll_result = poll(descriptors, 2, 25);
        if (poll_result < 0 && errno != EINTR) {
            wait_failure = errno;
            kill(-pid, SIGKILL);
        }
        if (output_open && (poll_result <= 0 || descriptors[0].revents)) {
            process_capture(output_pipe[0], output, &output_length, &total, maximum, &output_open, &overflow);
        }
        if (error_open && (poll_result <= 0 || descriptors[1].revents)) {
            process_capture(error_pipe[0], error_output, &error_length, &total, maximum, &error_open, &overflow);
        }
        pid_t waited = waitpid(pid, &status, WNOHANG);
        if (waited == pid) break;
        if (waited < 0 && errno != EINTR) {
            wait_failure = errno;
            break;
        }
    }
    process_capture(output_pipe[0], output, &output_length, &total, maximum, &output_open, &overflow);
    process_capture(error_pipe[0], error_output, &error_length, &total, maximum, &error_open, &overflow);
    close(output_pipe[0]);
    close(error_pipe[0]);
    slick_value result;
    if (cancelled_at) {
        result = slick_rt_result(0, make_class("std.process.Failure", "Operation", slick_rt_string("Cancelled", -1), "Program", a[0], "Message", slick_rt_string("operation cancelled; child process was signalled and reaped", -1), NULL));
    } else if (overflow) {
        char message[64];
        snprintf(message, sizeof(message), "captured output exceeds %" PRId64 " bytes", maximum);
        result = slick_rt_result(0, make_class("std.process.Failure", "Operation", slick_rt_string("OutputLimit", -1), "Program", a[0], "Message", slick_rt_string(message, -1), NULL));
    } else if (wait_failure) {
        result = slick_rt_result(0, make_class("std.process.Failure", "Operation", slick_rt_string("Wait", -1), "Program", a[0], "Message", slick_rt_string(strerror(wait_failure), -1), NULL));
    } else if (WIFSIGNALED(status)) {
        result = slick_rt_result(0, make_class("std.process.Failure", "Operation", slick_rt_string("Signal", -1), "Program", a[0], "Message", slick_rt_string("child process was terminated by a signal", -1), NULL));
    } else {
        result = slick_rt_result(1, make_class("std.process.Completed",
            "ExitCode", slick_rt_int(WEXITSTATUS(status)),
            "Output", slick_rt_bytes(output, output_length),
            "ErrorOutput", slick_rt_bytes(error_output, error_length), NULL));
    }
    free(output);
    free(error_output);
    return slick_ok(result);
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
    if (!out) return NULL;
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
    case 6: case 7: {
        int64_t n = slick_rt_array_len(value);
        for (int64_t i = 0; i < n; i++) {
            if (!slick_value_task_safe(slick_rt_array_index(value, i))) return 0;
        }
        return 1;
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
        slick_value *payload = (slick_value *)(uintptr_t)value.bits;
        return !payload || slick_value_task_safe(*payload);
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
    if (size && count > SIZE_MAX / size) return 0;
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
        uint8_t *grown = realloc(transfer->body, capacity);
        if (!grown) return 0;
        transfer->body = grown;
        transfer->capacity = capacity;
    }
    memcpy(transfer->body + transfer->length, data, n);
    transfer->length += n;
    return n;
}

static size_t http_read(char *buffer, size_t size, size_t count, void *opaque) {
    http_transfer *transfer = opaque;
    if (size && count > SIZE_MAX / size) return CURL_READFUNC_ABORT;
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
    if (size && count > SIZE_MAX / size) return 0;
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
        if (!items) return 0;
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
                char message[320];
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
                    char message[320];
                    snprintf(message, sizeof(message), "%s header value contains a forbidden control byte", name);
                    char *safe = sanitize_url(url);
                    slick_value failure = http_fail("InvalidRequest", safe, message, slick_rt_none());
                    free(safe); curl_slist_free_all(request_headers);
                    return slick_ok(slick_rt_result(0, failure));
                }
                size_t line_len = strlen(name) + 2 + (size_t)length;
                char *line = malloc(line_len + 1);
                if (!line) {
                    char *safe = sanitize_url(url);
                    slick_value failure = http_fail("Network", safe, "out of memory", slick_rt_none());
                    free(safe); curl_slist_free_all(request_headers);
                    return slick_ok(slick_rt_result(0, failure));
                }
                snprintf(line, line_len + 1, "%s: ", name);
                memcpy(line + strlen(name) + 2, raw, (size_t)length);
                line[line_len] = 0;
                struct curl_slist *grown_headers = curl_slist_append(request_headers, line);
                free(line);
                if (!grown_headers) {
                    char *safe = sanitize_url(url);
                    slick_value failure = http_fail("Network", safe, "out of memory", slick_rt_none());
                    free(safe); curl_slist_free_all(request_headers);
                    return slick_ok(slick_rt_result(0, failure));
                }
                request_headers = grown_headers;
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
    int stop;
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
    void *arena;
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
    for (struct addrinfo *candidate = addresses; candidate; candidate = candidate->ai_next) {
        fd = socket(candidate->ai_family, candidate->ai_socktype, candidate->ai_protocol);
        if (fd < 0) {
            continue;
        }
        int yes = 1;
        setsockopt(fd, SOL_SOCKET, SO_REUSEADDR, &yes, sizeof(yes));
        if (bind(fd, candidate->ai_addr, candidate->ai_addrlen) == 0 && listen(fd, 128) == 0) break;
        close(fd);
        fd = -1;
    }
    freeaddrinfo(addresses);
    if (fd < 0) {
        return slick_ok(slick_rt_result(0, http_server_fail("Bind", addr, "failed to bind listen address")));
    }
    http_server *srv = calloc(1, sizeof(*srv));
    if (!srv) {
        close(fd);
        return slick_ok(slick_rt_result(0, http_server_fail("Serve", addr, "out of memory")));
    }
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
        pthread_mutex_destroy(&srv->workers_mutex);
        free(srv);
        return slick_ok(slick_rt_result(0, http_server_fail("Serve", addr, "failed to start HTTP server")));
    }
    while (!cancelled(ctx) && !__atomic_load_n(&srv->stop, __ATOMIC_ACQUIRE)) usleep(10000);
    __atomic_store_n(&srv->stop, 1, __ATOMIC_RELEASE);
    shutdown(fd, SHUT_RDWR);
    close(fd);
    pthread_join(accept_thread, NULL);
    int64_t waited_ms = 0;
    int workers;
    do {
        pthread_mutex_lock(&srv->workers_mutex);
        workers = srv->workers;
        pthread_mutex_unlock(&srv->workers_mutex);
        if (workers == 0) break;
        usleep(10000);
        waited_ms += 10;
    } while (waited_ms < srv->shutdown_timeout_ms);
    while (workers != 0) {
        pthread_mutex_lock(&srv->workers_mutex);
        workers = srv->workers;
        pthread_mutex_unlock(&srv->workers_mutex);
        if (workers != 0) usleep(10000);
    }
    pthread_mutex_destroy(&srv->workers_mutex);
    free(srv);
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
    void *previous_arena = slick_rt_arena_enter(job->arena);
    job->outcome = slick_rt_iface_call(&job->ctx, job->server->handler, 0, 1, &job->request);
    slick_rt_arena_enter(previous_arena);
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

typedef struct {
    int fd;
    const uint8_t *buffer;
    size_t length;
    size_t offset;
} http_input;

static int http_input_read(http_input *input, void *destination, size_t length) {
    uint8_t *output = destination;
    while (length > 0) {
        if (input->offset < input->length) {
            size_t available = input->length - input->offset;
            size_t count = available < length ? available : length;
            memcpy(output, input->buffer + input->offset, count);
            input->offset += count;
            output += count;
            length -= count;
            continue;
        }
        ssize_t count = recv(input->fd, output, length, 0);
        if (count <= 0) return 0;
        output += (size_t)count;
        length -= (size_t)count;
    }
    return 1;
}

static int http_input_line(http_input *input, char *line, size_t capacity) {
    size_t length = 0;
    for (;;) {
        uint8_t byte;
        if (!http_input_read(input, &byte, 1)) return 0;
        if (byte == '\r') {
            if (!http_input_read(input, &byte, 1) || byte != '\n') return 0;
            line[length] = 0;
            return 1;
        }
        if (byte == '\n' || length + 1 >= capacity) return 0;
        line[length++] = (char)byte;
    }
}

// Returns 1 on success, 2 when the decoded body exceeds max_body, and 0 for
// malformed or incomplete framing.
static int http_read_chunked(http_input *input, int64_t max_body, int64_t max_header,
    uint8_t **body, int64_t *body_length) {
    uint8_t *output = NULL;
    size_t length = 0, capacity = 0, trailer_bytes = 0;
    char line[1024];
    for (;;) {
        if (!http_input_line(input, line, sizeof(line))) {
            free(output);
            return 0;
        }
        char *size_text = http_trim(line);
        char *extension = strchr(size_text, ';');
        if (extension) *extension = 0;
        size_text = http_trim(size_text);
        if (!*size_text) {
            free(output);
            return 0;
        }
        uint64_t chunk = 0;
        for (char *cursor = size_text; *cursor; cursor++) {
            unsigned digit;
            if (*cursor >= '0' && *cursor <= '9') digit = (unsigned)(*cursor - '0');
            else if (*cursor >= 'a' && *cursor <= 'f') digit = (unsigned)(*cursor - 'a' + 10);
            else if (*cursor >= 'A' && *cursor <= 'F') digit = (unsigned)(*cursor - 'A' + 10);
            else {
                free(output);
                return 0;
            }
            if (chunk > (UINT64_MAX - digit) / 16) {
                free(output);
                return 0;
            }
            chunk = chunk * 16 + digit;
        }
        if (chunk == 0) {
            for (;;) {
                if (!http_input_line(input, line, sizeof(line))) {
                    free(output);
                    return 0;
                }
                trailer_bytes += strlen(line) + 2;
                if (trailer_bytes > (size_t)max_header) {
                    free(output);
                    return 0;
                }
                if (!*line) {
                    *body = output;
                    *body_length = (int64_t)length;
                    return 1;
                }
            }
        }
        if (chunk > (uint64_t)max_body || length > (size_t)max_body - (size_t)chunk) {
            free(output);
            return 2;
        }
        size_t needed = length + (size_t)chunk;
        if (needed > capacity) {
            size_t grown = capacity ? capacity : ((size_t)max_body < 4096 ? (size_t)max_body : 4096);
            while (grown < needed) {
                size_t next = grown <= (size_t)max_body / 2 ? grown * 2 : (size_t)max_body;
                if (next <= grown) {
                    grown = needed;
                    break;
                }
                grown = next;
            }
            uint8_t *resized = realloc(output, grown);
            if (!resized) {
                free(output);
                fprintf(stderr, "slick: out of memory\n");
                abort();
            }
            output = resized;
            capacity = grown;
        }
        if (!http_input_read(input, output + length, (size_t)chunk)) {
            free(output);
            return 0;
        }
        length = needed;
        uint8_t ending[2];
        if (!http_input_read(input, ending, sizeof(ending)) || ending[0] != '\r' || ending[1] != '\n') {
            free(output);
            return 0;
        }
    }
}

static void *http_connection_run(void *argument) {
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
    int content_length_seen = 0, transfer_encoding_seen = 0, transfer_encoding_supported = 1;
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
        if (strcasecmp(name, "Transfer-Encoding") == 0) {
            transfer_encoding_seen++;
            if (strcasecmp(value, "chunked") != 0) transfer_encoding_supported = 0;
        }
        line = line_end + 2;
    }
    if ((transfer_encoding_seen && content_length_seen) || transfer_encoding_seen > 1 ||
        (transfer_encoding_seen && !transfer_encoding_supported)) {
        int unsupported = transfer_encoding_seen == 1 && !content_length_seen &&
            !transfer_encoding_supported;
        http_simple_response(fd, unsupported ? 501 : 400,
            unsupported ? "Not Implemented" : "Bad Request");
        for (size_t i = 0; i < parsed_count; i++) free(parsed[i].name);
        free(parsed);
        free(buffer);
        close(fd);
        http_worker_finished(server);
        return NULL;
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
    int64_t body_length = content_length;
    size_t available = used > header_bytes ? used - header_bytes : 0;
    if (transfer_encoding_seen) {
        http_input input = {
            .fd = fd,
            .buffer = (const uint8_t *)buffer + header_bytes,
            .length = available,
        };
        int framing = http_read_chunked(&input, server->max_body, server->max_header,
            &body, &body_length);
        if (framing != 1) {
            http_simple_response(fd, framing == 2 ? 413 : 400,
                framing == 2 ? "Payload Too Large" : "Bad Request");
            free(buffer);
            close(fd);
            http_worker_finished(server);
            return NULL;
        }
    } else if (content_length > 0) {
        body = malloc((size_t)content_length);
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
        "Body", slick_rt_bytes(body, body_length), NULL);
    free(target);
    free(body);

    http_handler_job job = {
        .server = server,
        .ctx = {0, NULL, server->ctx},
        .arena = slick_rt_arena_current(),
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
            if (cancelled(server->ctx) || __atomic_load_n(&server->stop, __ATOMIC_ACQUIRE)) {
                __atomic_store_n(&job.ctx.cancelled, 1, __ATOMIC_RELEASE);
            }
            char probe;
            ssize_t peeked = recv(fd, &probe, 1, MSG_PEEK | MSG_DONTWAIT);
            if (peeked == 0) {
                client_cancelled = 1;
                __atomic_store_n(&job.ctx.cancelled, 1, __ATOMIC_RELEASE);
            }
            usleep(10000);
        }
        pthread_join(handler_thread, NULL);
        if (!client_cancelled && !cancelled(server->ctx) &&
            !__atomic_load_n(&server->stop, __ATOMIC_ACQUIRE)) {
            http_send_handler_response(fd, method, job.outcome);
        }
    }
    pthread_mutex_destroy(&job.mutex);
    shutdown(fd, SHUT_RDWR);
    close(fd);
    http_worker_finished(server);
    return NULL;
}
static void *http_connection_main(void *argument) {
    void *arena = slick_rt_arena_push();
    void *result = http_connection_run(argument);
    slick_rt_arena_pop(arena);
    return result;
}


static void *http_server_loop(void *argument) {
    http_server *server = argument;
    while (!__atomic_load_n(&server->stop, __ATOMIC_ACQUIRE)) {
        struct sockaddr_storage address;
        socklen_t address_length = sizeof(address);
        int fd = accept(server->fd, (struct sockaddr *)&address, &address_length);
        if (fd < 0) {
            if (__atomic_load_n(&server->stop, __ATOMIC_ACQUIRE)) break;
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
typedef struct sqlite_transaction sqlite_transaction;
typedef struct {
    sqlite3 *db;
    int closed;
    pthread_mutex_t mu;
    sqlite_transaction *active_transaction;
} sdb;
struct sqlite_transaction {
    sdb *owner;
    int closed;
    int active;
};
typedef sqlite_transaction stx;
typedef struct { int32_t type_id; int32_t tag; int32_t field_count; slick_value *fields; } sqlite_union;

static slick_value sqlite_fail(const char *operation, int *code, const char *message) {
    slick_value value = code ? slick_rt_some(slick_rt_int(*code)) : slick_rt_none();
    return make_class("std.sqlite.Failure", "Operation", slick_rt_string(operation, -1),
        "Code", value, "Message", slick_rt_string(message, -1), NULL);
}

static int sqlite_cancelled(void *context) {
    return cancelled(context);
}
static int sqlite_lock(sdb *database, slick_ctx *context) {
    for (;;) {
        int result = pthread_mutex_trylock(&database->mu);
        if (result == 0) return 1;
        if (result != EBUSY || cancelled(context)) return 0;
        usleep(10000);
    }
}


static int sqlite_prepare_one(sqlite3 *database, slick_value sql_value, sqlite3_stmt **statement,
    const char **detail) {
    int64_t length = sv_len(sql_value);
    if (length <= 0) {
        *detail = "SQL statement must not be empty";
        return SQLITE_MISUSE;
    }
    if (length > INT_MAX) {
        *detail = "SQL statement is too large";
        return SQLITE_TOOBIG;
    }
    const char *sql = sv_str(sql_value), *tail = NULL;
    int result = sqlite3_prepare_v2(database, sql, (int)length, statement, &tail);
    if (result != SQLITE_OK) return result;
    if (!*statement) {
        *detail = "SQL statement must not be empty";
        return SQLITE_MISUSE;
    }
    const char *end = sql + length;
    while (tail < end) {
        sqlite3_stmt *extra = NULL;
        const char *next = NULL;
        result = sqlite3_prepare_v2(database, tail, (int)(end - tail), &extra, &next);
        if (result != SQLITE_OK) return result;
        if (extra) {
            sqlite3_finalize(extra);
            *detail = "statement contains multiple SQL statements";
            return SQLITE_MISUSE;
        }
        if (!next || next <= tail) break;
        tail = next;
    }
    return SQLITE_OK;
}

static int sqlite_bind_params(sqlite3_stmt *statement, slick_value parameters, const char **detail) {
    int64_t count = slick_rt_array_len(parameters);
    if (count != sqlite3_bind_parameter_count(statement)) {
        *detail = "parameter count does not match SQL placeholders";
        return SQLITE_RANGE;
    }
    for (int64_t index = 0; index < count; index++) {
        slick_value value = slick_rt_array_index(parameters, index);
        sqlite_union *variant = value.kind == 13 ? (sqlite_union *)(uintptr_t)value.bits : NULL;
        if (!variant || variant->tag < 1 || variant->tag > 5 ||
            (variant->tag == 1 ? variant->field_count != 0 : variant->field_count != 1)) {
            *detail = "invalid SQLite parameter value";
            return SQLITE_MISMATCH;
        }
        int result = SQLITE_MISMATCH;
        if (variant->tag == 1) {
            result = sqlite3_bind_null(statement, (int)index + 1);
        } else if (variant->tag == 2 && variant->fields[0].kind == 2) {
            result = sqlite3_bind_int64(statement, (int)index + 1, variant->fields[0].bits);
        } else if (variant->tag == 3 && variant->fields[0].kind == 3) {
            double number;
            memcpy(&number, &variant->fields[0].bits, sizeof(number));
            if (!isfinite(number)) {
                *detail = "cannot bind non-finite floating-point value";
                return SQLITE_MISMATCH;
            }
            result = sqlite3_bind_double(statement, (int)index + 1, number);
        } else if (variant->tag == 4 && variant->fields[0].kind == 4) {
            if (!utf8_valid(sv_data(variant->fields[0]), sv_len(variant->fields[0])).bits) {
                *detail = "text parameter contains invalid UTF-8";
                return SQLITE_MISMATCH;
            }
            result = sqlite3_bind_text64(statement, (int)index + 1, sv_str(variant->fields[0]),
                (sqlite3_uint64)sv_len(variant->fields[0]), SQLITE_TRANSIENT, SQLITE_UTF8);
        } else if (variant->tag == 5 && variant->fields[0].kind == 5) {
            result = sqlite3_bind_blob64(statement, (int)index + 1, sv_data(variant->fields[0]),
                (sqlite3_uint64)sv_len(variant->fields[0]), SQLITE_TRANSIENT);
        } else {
            *detail = "invalid SQLite parameter payload";
            return SQLITE_MISMATCH;
        }
        if (result != SQLITE_OK) return result;
    }
    return SQLITE_OK;
}

static slick_value sqlite_value(int type, sqlite3_stmt *statement, int column) {
    int union_id = slick_rt_union_id("std.sqlite.Value");
    if (type == SQLITE_NULL) return slick_rt_union(union_id, 1, 0, NULL);
    if (type == SQLITE_INTEGER) {
        slick_value value = slick_rt_int(sqlite3_column_int64(statement, column));
        return slick_rt_union(union_id, 2, 1, &value);
    }
    if (type == SQLITE_FLOAT) {
        slick_value value = slick_rt_float(sqlite3_column_double(statement, column));
        return slick_rt_union(union_id, 3, 1, &value);
    }
    int length = sqlite3_column_bytes(statement, column);
    if (type == SQLITE_BLOB) {
        slick_value value = slick_rt_bytes(sqlite3_column_blob(statement, column), length);
        return slick_rt_union(union_id, 5, 1, &value);
    }
    slick_value value = slick_rt_string((const char *)sqlite3_column_text(statement, column), length);
    return slick_rt_union(union_id, 4, 1, &value);
}

slick_outcome slick_nat_sqlite_open(slick_ctx *context, slick_value *arguments) {
    if (cancelled(context)) {
        return slick_ok(slick_rt_result(0, sqlite_fail("Open", NULL, "operation cancelled")));
    }
    if (memchr(sv_data(arguments[0]), 0, (size_t)sv_len(arguments[0]))) {
        return slick_ok(slick_rt_result(0, sqlite_fail("Open", NULL, "path contains NUL")));
    }
    const char *path = sv_str(arguments[0]);
    if (strcmp(path, ":memory:") != 0) {
        char *parent = strdup(path);
        char *slash = strrchr(parent, '/');
        if (slash) {
            if (slash == parent) slash[1] = 0;
            else *slash = 0;
            struct stat status;
            if (strcmp(parent, ".") != 0 && strcmp(parent, "/") != 0 &&
                (stat(parent, &status) != 0 || !S_ISDIR(status.st_mode))) {
                size_t length = strlen(parent) + 38;
                char *message = malloc(length);
                snprintf(message, length, "parent directory does not exist: %s", parent);
                slick_value failure = sqlite_fail("Open", NULL, message);
                free(message);
                free(parent);
                return slick_ok(slick_rt_result(0, failure));
            }
        }
        free(parent);
    }
    sqlite3 *database = NULL;
    int result = sqlite3_open_v2(sv_str(arguments[0]), &database,
        SQLITE_OPEN_READWRITE | SQLITE_OPEN_CREATE | SQLITE_OPEN_FULLMUTEX, NULL);
    if (result != SQLITE_OK) {
        int code = result;
        slick_value failure = sqlite_fail("Open", &code, database ? sqlite3_errmsg(database) : "open failed");
        if (database) sqlite3_close(database);
        return slick_ok(slick_rt_result(0, failure));
    }
    sdb *handle = calloc(1, sizeof(*handle));
    if (!handle) {
        sqlite3_close(database);
        return slick_ok(slick_rt_result(0, sqlite_fail("Open", NULL, "out of memory")));
    }
    handle->db = database;
    pthread_mutex_init(&handle->mu, NULL);
    slick_value object = make_class("std.sqlite.Database", NULL);
    set_resource(object, handle);
    return slick_ok(slick_rt_result(1, object));
}

static slick_outcome sqlite_execute(slick_ctx *context, sdb *database, slick_value descriptor,
    stx *transaction) {
    if (!database) return slick_ok(slick_rt_result(0, sqlite_fail("Execute", NULL, "database is closed")));
    if (!sqlite_lock(database, context)) {
        return slick_ok(slick_rt_result(0, sqlite_fail("Execute", NULL, "operation cancelled")));
    }
    if ((transaction && !transaction->active) || database->closed ||
        (!transaction && database->active_transaction)) {
        const char *message = transaction && !transaction->active ? "transaction is closed" :
            database->closed ? "database is closed" : "a transaction is already active";
        pthread_mutex_unlock(&database->mu);
        return slick_ok(slick_rt_result(0, sqlite_fail("Execute", NULL, message)));
    }
    if (cancelled(context)) {
        pthread_mutex_unlock(&database->mu);
        return slick_ok(slick_rt_result(0, sqlite_fail("Execute", NULL, "operation cancelled")));
    }
    sqlite3_progress_handler(database->db, 1000, sqlite_cancelled, context);
    sqlite3_stmt *statement = NULL;
    const char *detail = NULL;
    int result = sqlite_prepare_one(database->db, class_field(descriptor, "SQL"), &statement, &detail);
    if (result == SQLITE_OK) result = sqlite_bind_params(statement, class_field(descriptor, "Parameters"), &detail);
    if (result == SQLITE_OK) result = sqlite3_step(statement);
    if (result == SQLITE_ROW) result = SQLITE_DONE;
    if (result != SQLITE_DONE) {
        int code = result;
        int *failure_code = detail || cancelled(context) ? NULL : &code;
        const char *message = cancelled(context) ? "operation cancelled" : (detail ? detail : sqlite3_errmsg(database->db));
        if (statement) sqlite3_finalize(statement);
        sqlite3_progress_handler(database->db, 0, NULL, NULL);
        pthread_mutex_unlock(&database->mu);
        return slick_ok(slick_rt_result(0, sqlite_fail("Execute", failure_code, message)));
    }
    int64_t changes = sqlite3_changes64(database->db);
    sqlite3_int64 inserted = sqlite3_last_insert_rowid(database->db);
    sqlite3_finalize(statement);
    sqlite3_progress_handler(database->db, 0, NULL, NULL);
    pthread_mutex_unlock(&database->mu);
    return slick_ok(slick_rt_result(1, make_class("std.sqlite.Execution",
        "RowsAffected", slick_rt_int(changes), "LastInsertId", slick_rt_some(slick_rt_int(inserted)), NULL)));
}

static slick_outcome sqlite_query(slick_ctx *context, sdb *database, slick_value descriptor,
    stx *transaction) {
    if (!database) return slick_ok(slick_rt_result(0, sqlite_fail("Query", NULL, "database is closed")));
    int64_t maximum_rows = class_field(descriptor, "MaxRows").bits;
    int64_t maximum_bytes = class_field(descriptor, "MaxBytes").bits;
    if (maximum_rows <= 0 || maximum_bytes <= 0) {
        return slick_ok(slick_rt_result(0, sqlite_fail("Query", NULL, "MaxRows and MaxBytes must be greater than zero")));
    }
    if (!sqlite_lock(database, context)) {
        return slick_ok(slick_rt_result(0, sqlite_fail("Query", NULL, "operation cancelled")));
    }
    if ((transaction && !transaction->active) || database->closed ||
        (!transaction && database->active_transaction)) {
        const char *message = transaction && !transaction->active ? "transaction is closed" :
            database->closed ? "database is closed" : "a transaction is already active";
        pthread_mutex_unlock(&database->mu);
        return slick_ok(slick_rt_result(0, sqlite_fail("Query", NULL, message)));
    }
    if (cancelled(context)) {
        pthread_mutex_unlock(&database->mu);
        return slick_ok(slick_rt_result(0, sqlite_fail("Query", NULL, "operation cancelled")));
    }
    sqlite3_progress_handler(database->db, 1000, sqlite_cancelled, context);
    sqlite3_stmt *statement = NULL;
    const char *detail = NULL;
    char detail_buffer[256];
    int result = sqlite_prepare_one(database->db, class_field(descriptor, "SQL"), &statement, &detail);
    if (result == SQLITE_OK) result = sqlite_bind_params(statement, class_field(descriptor, "Parameters"), &detail);
    int columns = result == SQLITE_OK ? sqlite3_column_count(statement) : 0;
    for (int left = 0; result == SQLITE_OK && left < columns; left++) {
        for (int right = left + 1; right < columns; right++) {
            if (strcmp(sqlite3_column_name(statement, left), sqlite3_column_name(statement, right)) == 0) {
                snprintf(detail_buffer, sizeof(detail_buffer),
                    "query returned duplicate column name %c%s%c; use SQL aliases",
                    '"', sqlite3_column_name(statement, left), '"');
                detail = detail_buffer;
                result = SQLITE_MISMATCH;
                break;
            }
        }
    }
    slick_value *rows = NULL;
    int64_t row_count = 0, byte_count = 0;
    while (result == SQLITE_OK && (result = sqlite3_step(statement)) == SQLITE_ROW) {
        row_count++;
        if (row_count > maximum_rows) {
            snprintf(detail_buffer, sizeof(detail_buffer),
                "query exceeded maximum row limit of %" PRId64, maximum_rows);
            detail = detail_buffer;
            result = SQLITE_TOOBIG;
            break;
        }
        slick_value map = slick_rt_empty_map();
        for (int column = 0; column < columns; column++) {
            int type = sqlite3_column_type(statement, column);
            if (type == SQLITE_FLOAT && !isfinite(sqlite3_column_double(statement, column))) {
                detail = "query returned a non-finite floating-point value";
                result = SQLITE_MISMATCH;
                break;
            }
            if (type == SQLITE_TEXT && !utf8_valid(sqlite3_column_text(statement, column),
                    sqlite3_column_bytes(statement, column)).bits) {
                detail = "query returned invalid UTF-8 text";
                result = SQLITE_MISMATCH;
                break;
            }
            int64_t size = type == SQLITE_TEXT || type == SQLITE_BLOB ?
                sqlite3_column_bytes(statement, column) : 8;
            if (size > maximum_bytes - byte_count) {
                snprintf(detail_buffer, sizeof(detail_buffer),
                    "query exceeded maximum byte limit of %" PRId64, maximum_bytes);
                detail = detail_buffer;
                result = SQLITE_TOOBIG;
                break;
            }
            byte_count += size;
            map = slick_rt_map_with(map, slick_rt_string(sqlite3_column_name(statement, column), -1),
                sqlite_value(type, statement, column));
        }
        if (result != SQLITE_ROW) break;
        slick_value *grown = realloc(rows, sizeof(*rows) * (size_t)row_count);
        if (!grown) {
            detail = "out of memory";
            result = SQLITE_NOMEM;
            break;
        }
        rows = grown;
        rows[row_count - 1] = make_class("std.sqlite.Row", "Values", map, NULL);
        result = SQLITE_OK;
    }
    if (result == SQLITE_DONE) result = SQLITE_OK;
    if (result != SQLITE_OK) {
        int code = result;
        int *failure_code = detail || cancelled(context) ? NULL : &code;
        const char *message = cancelled(context) ? "operation cancelled" : (detail ? detail : sqlite3_errmsg(database->db));
        if (statement) sqlite3_finalize(statement);
        sqlite3_progress_handler(database->db, 0, NULL, NULL);
        pthread_mutex_unlock(&database->mu);
        free(rows);
        return slick_ok(slick_rt_result(0, sqlite_fail("Query", failure_code, message)));
    }
    sqlite3_finalize(statement);
    sqlite3_progress_handler(database->db, 0, NULL, NULL);
    pthread_mutex_unlock(&database->mu);
    slick_value output = slick_rt_array(6, row_count, rows);
    free(rows);
    return slick_ok(slick_rt_result(1, output));
}

slick_outcome slick_nat_sqlite_db_exec(slick_ctx *context, slick_value *arguments) {
    return sqlite_execute(context, get_resource(arguments[0]), arguments[1], NULL);
}
slick_outcome slick_nat_sqlite_db_query(slick_ctx *context, slick_value *arguments) {
    return sqlite_query(context, get_resource(arguments[0]), arguments[1], NULL);
}
slick_outcome slick_nat_sqlite_db_begin(slick_ctx *context, slick_value *arguments) {
    sdb *database = get_resource(arguments[0]);
    if (!database) return slick_ok(slick_rt_result(0, sqlite_fail("Begin", NULL, "database is closed")));
    if (!sqlite_lock(database, context)) {
        return slick_ok(slick_rt_result(0, sqlite_fail("Begin", NULL, "operation cancelled")));
    }
    if (database->closed || database->active_transaction || cancelled(context)) {
        const char *message = database->closed ? "database is closed" :
            database->active_transaction ? "a transaction is already active" : "operation cancelled";
        pthread_mutex_unlock(&database->mu);
        return slick_ok(slick_rt_result(0, sqlite_fail("Begin", NULL, message)));
    }
    char *message = NULL;
    int result = sqlite3_exec(database->db, "BEGIN", NULL, NULL, &message);
    if (result != SQLITE_OK) {
        int code = result;
        slick_value failure = sqlite_fail("Begin", &code, message ? message : sqlite3_errmsg(database->db));
        sqlite3_free(message);
        pthread_mutex_unlock(&database->mu);
        return slick_ok(slick_rt_result(0, failure));
    }
    stx *transaction = calloc(1, sizeof(*transaction));
    if (!transaction) {
        sqlite3_exec(database->db, "ROLLBACK", NULL, NULL, NULL);
        pthread_mutex_unlock(&database->mu);
        return slick_ok(slick_rt_result(0, sqlite_fail("Begin", NULL, "out of memory")));
    }
    transaction->owner = database;
    transaction->active = 1;
    database->active_transaction = transaction;
    pthread_mutex_unlock(&database->mu);
    slick_value object = make_class("std.sqlite.Transaction", NULL);
    set_resource(object, transaction);
    return slick_ok(slick_rt_result(1, object));
}

slick_outcome slick_nat_sqlite_db_close(slick_ctx *context, slick_value *arguments) {
    (void)context;
    sdb *database = get_resource(arguments[0]);
    if (!database) return slick_ok((slick_value){0, 0, 0});
    pthread_mutex_lock(&database->mu);
    if (database->closed) {
        pthread_mutex_unlock(&database->mu);
        return slick_ok((slick_value){0, 0, 0});
    }
    database->closed = 1;
    if (database->active_transaction) {
        database->active_transaction->active = 0;
        database->active_transaction->closed = 1;
        sqlite3_exec(database->db, "ROLLBACK", NULL, NULL, NULL);
        database->active_transaction = NULL;
    }
    int result = sqlite3_close(database->db);
    if (result != SQLITE_OK) {
        int code = result;
        slick_value failure = sqlite_fail("Close", &code, sqlite3_errstr(result));
        pthread_mutex_unlock(&database->mu);
        return slick_throw(failure);
    }
    pthread_mutex_unlock(&database->mu);
    return slick_ok((slick_value){0, 0, 0});
}

slick_outcome slick_nat_sqlite_tx_exec(slick_ctx *context, slick_value *arguments) {
    stx *transaction = get_resource(arguments[0]);
    if (!transaction) return slick_ok(slick_rt_result(0, sqlite_fail("Execute", NULL, "transaction is closed")));
    return sqlite_execute(context, transaction->owner, arguments[1], transaction);
}
slick_outcome slick_nat_sqlite_tx_query(slick_ctx *context, slick_value *arguments) {
    stx *transaction = get_resource(arguments[0]);
    if (!transaction) return slick_ok(slick_rt_result(0, sqlite_fail("Query", NULL, "transaction is closed")));
    return sqlite_query(context, transaction->owner, arguments[1], transaction);
}

static slick_outcome sqlite_transaction_end(slick_ctx *context, stx *transaction,
    const char *sql, const char *operation) {
    if (!transaction) {
        return slick_ok(slick_rt_result(0, sqlite_fail(operation, NULL, "transaction is closed")));
    }
    sdb *database = transaction->owner;
    if (!sqlite_lock(database, context)) {
        return slick_ok(slick_rt_result(0, sqlite_fail(operation, NULL, "operation cancelled")));
    }
    if (!transaction->active || database->closed) {
        pthread_mutex_unlock(&database->mu);
        return slick_ok(slick_rt_result(0, sqlite_fail(operation, NULL, "transaction is closed")));
    }
    if (cancelled(context) && strcmp(operation, "Close") != 0) {
        pthread_mutex_unlock(&database->mu);
        return slick_ok(slick_rt_result(0, sqlite_fail(operation, NULL, "operation cancelled")));
    }
    char *message = NULL;
    int result = sqlite3_exec(database->db, sql, NULL, NULL, &message);
    transaction->active = 0;
    if (database->active_transaction == transaction) database->active_transaction = NULL;
    pthread_mutex_unlock(&database->mu);
    if (result != SQLITE_OK) {
        int code = result;
        slick_value failure = sqlite_fail(operation, &code, message ? message : sqlite3_errstr(result));
        sqlite3_free(message);
        return slick_ok(slick_rt_result(0, failure));
    }
    return slick_ok(slick_rt_result(1, (slick_value){0, 0, 0}));
}

slick_outcome slick_nat_sqlite_tx_commit(slick_ctx *context, slick_value *arguments) {
    return sqlite_transaction_end(context, get_resource(arguments[0]), "COMMIT", "Commit");
}
slick_outcome slick_nat_sqlite_tx_rollback(slick_ctx *context, slick_value *arguments) {
    return sqlite_transaction_end(context, get_resource(arguments[0]), "ROLLBACK", "Rollback");
}
slick_outcome slick_nat_sqlite_tx_close(slick_ctx *context, slick_value *arguments) {
    (void)context;
    stx *transaction = get_resource(arguments[0]);
    if (!transaction) return slick_ok((slick_value){0, 0, 0});
    sdb *database = transaction->owner;
    pthread_mutex_lock(&database->mu);
    if (transaction->closed) {
        pthread_mutex_unlock(&database->mu);
        return slick_ok((slick_value){0, 0, 0});
    }
    transaction->closed = 1;
    int result = SQLITE_OK;
    char *message = NULL;
    if (transaction->active && !database->closed) {
        result = sqlite3_exec(database->db, "ROLLBACK", NULL, NULL, &message);
    }
    transaction->active = 0;
    if (database->active_transaction == transaction) database->active_transaction = NULL;
    pthread_mutex_unlock(&database->mu);
    if (result != SQLITE_OK) {
        int code = result;
        slick_value failure = sqlite_fail("Close", &code,
            message ? message : sqlite3_errstr(result));
        sqlite3_free(message);
        return slick_throw(failure);
    }
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
