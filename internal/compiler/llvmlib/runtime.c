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
#ifdef SLICK_HAS_JSON
#include <jansson.h>
#endif

enum {
    SLICK_NULL = 0,
    SLICK_BOOL = 1,
    SLICK_INT = 2,
    SLICK_FLOAT = 3,
    SLICK_STRING = 4,
    SLICK_BYTES = 5,
    SLICK_ARRAY = 6,
    SLICK_TUPLE = 7,
    SLICK_MAP = 8,
    SLICK_BUFFER = 9,
    SLICK_OPTIONAL = 10,
    SLICK_RESULT = 11,
    SLICK_CLASS = 12,
    SLICK_UNION = 13,
    SLICK_INTERFACE = 14,
    SLICK_CALLABLE = 15,
    SLICK_ITERABLE = 16,
    SLICK_ERROR = 17
};

enum {
    SLICK_OK = 0,
    SLICK_THROW = 1,
    SLICK_RETURN = 2,
    SLICK_BREAK = 3,
    SLICK_CONTINUE = 4,
    SLICK_CANCEL = 5
};

enum {
    SLICK_ITER_ARRAY = 1,
    SLICK_ITER_RANGE = 2,
    SLICK_ITER_ENUM = 3,
    SLICK_ITER_ZIP = 4,
    SLICK_ITER_MAP = 5
};

typedef struct slick_value {
    int32_t kind;
    int32_t flags;
    int64_t bits;
} slick_value;

typedef struct slick_outcome {
    int32_t code;
    int32_t pad;
    slick_value value;
} slick_outcome;

typedef slick_outcome (*slick_fn)(void *ctx, slick_value *args);
typedef void (*slick_task_fn)(slick_outcome *ret, void *ctx, slick_value *args);

typedef struct slick_task slick_task;
typedef struct slick_scope slick_scope;

typedef struct slick_ctx {
    volatile int cancelled;
    slick_scope *scope;
    struct slick_ctx *parent;
} slick_ctx;

typedef struct slick_bytes {
    int64_t len;
    uint8_t *data;
} slick_bytes;

typedef struct slick_array {
    int64_t len;
    slick_value *items;
} slick_array;

typedef struct slick_map_entry {
    slick_value key;
    slick_value value;
} slick_map_entry;

typedef struct slick_map {
    int64_t len;
    slick_map_entry *entries;
} slick_map;

typedef struct slick_buffer {
    int64_t len;
    int64_t cap;
    slick_value *items;
} slick_buffer;

typedef struct slick_pair {
    slick_value a;
    slick_value b;
} slick_pair;

typedef struct slick_class {
    int32_t type_id;
    int32_t field_count;
    void *resource;
    slick_value *fields;
    slick_value error_message;
    slick_value *suppressed;
    int32_t suppressed_count;
    int32_t suppressed_capacity;
} slick_class;

typedef struct slick_union {
    int32_t type_id;
    int32_t tag;
    int32_t field_count;
    slick_value *fields;
} slick_union_obj;

typedef struct slick_iface {
    int32_t type_id;
    int32_t method_count;
    slick_value receiver;
    slick_fn *vtable;
} slick_iface;

typedef struct slick_callable {
    slick_fn code;
    int32_t capture_count;
    int32_t param_count;
    slick_value *captures;
} slick_callable;

typedef struct slick_iter {
    int32_t kind;
    int32_t width;
    int64_t start;
    int64_t length;
    slick_value source;
    slick_value extra;
} slick_iter;

typedef struct slick_type_info {
    const char *name;
    int32_t field_count;
    const char **field_names;
    const char **json_names;
    const char **field_schemas;
    int32_t is_error;
    int32_t native_resource;
} slick_type_info;

typedef struct slick_io {
    int kind; /* 1 reader, 2 writer */
    int closed;
    int64_t pos;
    slick_bytes data;
} slick_io;

typedef struct slick_tmpdir {
    int closed;
    char *path;
} slick_tmpdir;


typedef struct slick_arena slick_arena;

struct slick_task {
    pthread_t thread;
    slick_task_fn fn;
    slick_value *args;
    int argc;
    slick_ctx ctx;
    slick_outcome result;
    int done;
    int consumed;
    pthread_mutex_t mu;
    pthread_cond_t cv;
    slick_task *next;
    slick_arena *arena;
    slick_arena *parent_arena;
    int arena_joined;
    int started;
};

struct slick_scope {
    slick_ctx *parent;
    slick_task *children;
    pthread_mutex_t mu;
};
const int slick_abi_version_1 = 1;

static const slick_type_info *slick_types;
static int slick_type_count;

static slick_value slick_null(void) {
    slick_value v = {SLICK_NULL, 0, 0};
    return v;
}

static slick_outcome slick_ok(slick_value v) {
    slick_outcome o;
    o.code = SLICK_OK;
    o.pad = 0;
    o.value = v;
    return o;
}

static slick_outcome slick_throw_val(slick_value v) {
    slick_outcome o;
    o.code = SLICK_THROW;
    o.pad = 0;
    o.value = v;
    return o;
}

typedef struct slick_managed_block {
    void *pointer;
    struct slick_managed_block *next;
} slick_managed_block;

struct slick_arena {
    pthread_mutex_t mutex;
    slick_managed_block *blocks;
    slick_arena *parent;
};

static slick_arena slick_root_arena = {PTHREAD_MUTEX_INITIALIZER, NULL, NULL};
static _Thread_local slick_arena *slick_current_arena;
static pthread_once_t slick_managed_once = PTHREAD_ONCE_INIT;

static void slick_arena_release(slick_arena *arena) {
    pthread_mutex_lock(&arena->mutex);
    slick_managed_block *block = arena->blocks;
    arena->blocks = NULL;
    pthread_mutex_unlock(&arena->mutex);
    while (block) {
        slick_managed_block *next = block->next;
        free(block->pointer);
        free(block);
        block = next;
    }
}

static void slick_release_managed(void) {
    slick_arena_release(&slick_root_arena);
}

static void slick_managed_init(void) {
    atexit(slick_release_managed);
}

static slick_arena *slick_active_arena(void) {
    pthread_once(&slick_managed_once, slick_managed_init);
    return slick_current_arena ? slick_current_arena : &slick_root_arena;
}

void *slick_rt_arena_push(void) {
    slick_arena *arena = calloc(1, sizeof(*arena));
    if (!arena) {
        fprintf(stderr, "slick: out of memory\n");
        abort();
    }
    pthread_mutex_init(&arena->mutex, NULL);
    arena->parent = slick_active_arena();
    slick_current_arena = arena;
    return arena;
}

void slick_rt_arena_pop(void *handle) {
    slick_arena *arena = handle;
    if (!arena) return;
    slick_current_arena = arena->parent;
    slick_arena_release(arena);
    pthread_mutex_destroy(&arena->mutex);
    free(arena);
}
void *slick_rt_arena_current(void) {
    return slick_active_arena();
}

void *slick_rt_arena_enter(void *handle) {
    slick_arena *previous = slick_current_arena;
    slick_current_arena = handle;
    return previous;
}


static void slick_arena_merge(slick_arena *parent, slick_arena *child) {
    pthread_mutex_lock(&parent->mutex);
    pthread_mutex_lock(&child->mutex);
    if (child->blocks) {
        slick_managed_block *tail = child->blocks;
        while (tail->next) tail = tail->next;
        tail->next = parent->blocks;
        parent->blocks = child->blocks;
        child->blocks = NULL;
    }
    pthread_mutex_unlock(&child->mutex);
    pthread_mutex_unlock(&parent->mutex);
}

static void slick_track_managed(void *pointer) {
    slick_managed_block *block = malloc(sizeof(*block));
    if (!block) {
        fprintf(stderr, "slick: out of memory\n");
        abort();
    }
    block->pointer = pointer;
    slick_arena *arena = slick_active_arena();
    pthread_mutex_lock(&arena->mutex);
    block->next = arena->blocks;
    arena->blocks = block;
    pthread_mutex_unlock(&arena->mutex);
}

static void *slick_xmalloc(size_t n) {
    void *p = calloc(1, n ? n : 1);
    if (!p) {
        fprintf(stderr, "slick: out of memory\n");
        abort();
    }
    slick_track_managed(p);
    return p;
}

static slick_managed_block *slick_find_managed(slick_arena *arena, void *pointer) {
    slick_managed_block *block = arena->blocks;
    while (block && block->pointer != pointer) block = block->next;
    return block;
}

static void *slick_xrealloc(void *p, size_t n) {
    if (!p) return slick_xmalloc(n);
    for (slick_arena *arena = slick_active_arena(); arena; arena = arena->parent) {
        pthread_mutex_lock(&arena->mutex);
        slick_managed_block *block = slick_find_managed(arena, p);
        if (!block) {
            pthread_mutex_unlock(&arena->mutex);
            continue;
        }
        void *q = realloc(p, n ? n : 1);
        if (!q) {
            pthread_mutex_unlock(&arena->mutex);
            fprintf(stderr, "slick: out of memory\n");
            abort();
        }
        block->pointer = q;
        pthread_mutex_unlock(&arena->mutex);
        return q;
    }
    void *q = realloc(p, n ? n : 1);
    if (!q) {
        fprintf(stderr, "slick: out of memory\n");
        abort();
    }
    slick_track_managed(q);
    return q;
}

static void slick_xfree(void *pointer) {
    if (!pointer) return;
    for (slick_arena *arena = slick_active_arena(); arena; arena = arena->parent) {
        pthread_mutex_lock(&arena->mutex);
        slick_managed_block **link = &arena->blocks;
        while (*link && (*link)->pointer != pointer) link = &(*link)->next;
        if (!*link) {
            pthread_mutex_unlock(&arena->mutex);
            continue;
        }
        slick_managed_block *block = *link;
        *link = block->next;
        pthread_mutex_unlock(&arena->mutex);
        free(block);
        free(pointer);
        return;
    }
    free(pointer);
}

static char *slick_xstrdup(const char *s) {
    size_t n = strlen(s);
    char *p = slick_xmalloc(n + 1);
    memcpy(p, s, n + 1);
    return p;
}

slick_value slick_rt_bool(int32_t v) {
    slick_value out = {SLICK_BOOL, 0, v ? 1 : 0};
    return out;
}

slick_value slick_rt_int(int64_t v) {
    slick_value out = {SLICK_INT, 0, v};
    return out;
}

slick_value slick_rt_float(double v) {
    slick_value out = {SLICK_FLOAT, 0, 0};
    memcpy(&out.bits, &v, sizeof(double));
    return out;
}

static double slick_as_float(slick_value v) {
    double d;
    memcpy(&d, &v.bits, sizeof(double));
    return d;
}

static slick_bytes *slick_new_bytes(const void *data, int64_t len) {
    slick_bytes *b = slick_xmalloc(sizeof(*b));
    b->len = len;
    b->data = slick_xmalloc((size_t)len + 1);
    if (len > 0) memcpy(b->data, data, (size_t)len);
    b->data[len] = 0;
    return b;
}

slick_value slick_rt_string(const char *s, int64_t len) {
    if (len < 0) {
        len = (int64_t)strlen(s);
    }
    slick_value out = {SLICK_STRING, 0, 0};
    out.bits = (int64_t)(uintptr_t)slick_new_bytes(s, len);
    return out;
}

slick_value slick_rt_bytes(const void *data, int64_t len) {
    slick_value out = {SLICK_BYTES, 0, 0};
    out.bits = (int64_t)(uintptr_t)slick_new_bytes(data, len);
    return out;
}

static slick_bytes *slick_as_bytes(slick_value v) {
    return (slick_bytes *)(uintptr_t)v.bits;
}

static const char *slick_cstr(slick_value v) {
    slick_bytes *b = slick_as_bytes(v);
    if (!b || !b->data) {
        return "";
    }
    return (const char *)b->data;
}

static int64_t slick_clen(slick_value v) {
    slick_bytes *b = slick_as_bytes(v);
    return b ? b->len : 0;
}

slick_value slick_rt_array(int32_t kind, int64_t n, slick_value *items) {
    slick_array *a = slick_xmalloc(sizeof(*a));
    a->len = n;
    if (n > 0) {
        a->items = slick_xmalloc(sizeof(slick_value) * (size_t)n);
        memcpy(a->items, items, sizeof(slick_value) * (size_t)n);
    }
    slick_value out = {kind, 0, (int64_t)(uintptr_t)a};
    return out;
}

static slick_array *slick_as_array(slick_value v) {
    return (slick_array *)(uintptr_t)v.bits;
}

slick_value slick_rt_optional(int present, slick_value payload) {
    slick_value *box = slick_xmalloc(sizeof(*box));
    *box = payload;
    slick_value out = {SLICK_OPTIONAL, present ? 1 : 0, (int64_t)(uintptr_t)box};
    return out;
}

slick_value slick_rt_none(void) {
    return slick_rt_optional(0, slick_null());
}

slick_value slick_rt_some(slick_value payload) {
    return slick_rt_optional(1, payload);
}

slick_value slick_rt_result(int ok, slick_value payload) {
    slick_value *box = slick_xmalloc(sizeof(*box));
    *box = payload;
    slick_value out = {SLICK_RESULT, ok ? 1 : 0, (int64_t)(uintptr_t)box};
    return out;
}

static slick_value slick_payload(slick_value v) {
    slick_value *box = (slick_value *)(uintptr_t)v.bits;
    return box ? *box : slick_null();
}

slick_value slick_rt_class(int32_t type_id, int32_t n, slick_value *fields) {
    slick_class *c = slick_xmalloc(sizeof(*c));
    c->type_id = type_id;
    c->field_count = n;
    if (n > 0) {
        c->fields = slick_xmalloc(sizeof(slick_value) * (size_t)n);
        memcpy(c->fields, fields, sizeof(slick_value) * (size_t)n);
    }
    int is_error = 0;
    if (type_id >= 0 && type_id < slick_type_count) {
        is_error = slick_types[type_id].is_error;
    }
    slick_value out = {is_error ? SLICK_ERROR : SLICK_CLASS, 0, (int64_t)(uintptr_t)c};
    return out;
}

static slick_class *slick_as_class(slick_value v) {
    return (slick_class *)(uintptr_t)v.bits;
}

slick_value slick_rt_union(int32_t type_id, int32_t tag, int32_t n, slick_value *fields) {
    slick_union_obj *u = slick_xmalloc(sizeof(*u));
    u->type_id = type_id;
    u->tag = tag;
    u->field_count = n;
    if (n > 0) {
        u->fields = slick_xmalloc(sizeof(slick_value) * (size_t)n);
        memcpy(u->fields, fields, sizeof(slick_value) * (size_t)n);
    }
    slick_value out = {SLICK_UNION, tag, (int64_t)(uintptr_t)u};
    return out;
}

static slick_union_obj *slick_as_union(slick_value v) {
    return (slick_union_obj *)(uintptr_t)v.bits;
}

slick_value slick_rt_callable(slick_fn code, int32_t captures, slick_value *vals, int32_t params) {
    slick_callable *c = slick_xmalloc(sizeof(*c));
    c->code = code;
    c->capture_count = captures;
    c->param_count = params;
    if (captures > 0) {
        c->captures = slick_xmalloc(sizeof(slick_value) * (size_t)captures);
        memcpy(c->captures, vals, sizeof(slick_value) * (size_t)captures);
    }
    slick_value out = {SLICK_CALLABLE, 0, (int64_t)(uintptr_t)c};
    return out;
}

static slick_callable *slick_as_callable(slick_value v) {
    return (slick_callable *)(uintptr_t)v.bits;
}

slick_value slick_rt_iface(int32_t type_id, slick_value recv, int32_t n, slick_fn *vtable) {
    slick_iface *i = slick_xmalloc(sizeof(*i));
    i->type_id = type_id;
    i->method_count = n;
    i->receiver = recv;
    i->vtable = vtable;
    slick_value out = {SLICK_INTERFACE, 0, (int64_t)(uintptr_t)i};
    return out;
}

static slick_iface *slick_as_iface(slick_value v) {
    return (slick_iface *)(uintptr_t)v.bits;
}

slick_value slick_rt_iface_receiver(slick_value value) {
    return value.kind == SLICK_INTERFACE ? slick_as_iface(value)->receiver : value;
}

slick_value slick_rt_iter_range(int64_t start, int64_t end) {
    slick_iter *it = slick_xmalloc(sizeof(*it));
    it->kind = SLICK_ITER_RANGE;
    it->width = 1;
    it->start = start;
    it->length = end > start ? end - start : 0;
    slick_value out = {SLICK_ITERABLE, 0, (int64_t)(uintptr_t)it};
    return out;
}

slick_value slick_rt_iter_of(slick_value source) {
    if (source.kind == SLICK_ITERABLE) {
        return source;
    }
    slick_iter *it = slick_xmalloc(sizeof(*it));
    it->kind = SLICK_ITER_ARRAY;
    it->width = source.kind == SLICK_TUPLE ? 0 : 1;
    it->source = source;
    if (source.kind == SLICK_ARRAY || source.kind == SLICK_TUPLE) {
        it->length = slick_as_array(source)->len;
    } else if (source.kind == SLICK_MAP) {
        it->kind = SLICK_ITER_MAP;
        it->width = 2;
        it->length = ((slick_map *)(uintptr_t)source.bits)->len;
    }
    slick_value out = {SLICK_ITERABLE, 0, (int64_t)(uintptr_t)it};
    return out;
}

slick_value slick_rt_iter_enum(slick_value source) {
    slick_iter *it = slick_xmalloc(sizeof(*it));
    it->kind = SLICK_ITER_ENUM;
    it->width = 2;
    it->source = slick_rt_iter_of(source);
    it->length = ((slick_iter *)(uintptr_t)it->source.bits)->length;
    slick_value out = {SLICK_ITERABLE, 0, (int64_t)(uintptr_t)it};
    return out;
}

slick_value slick_rt_iter_zip(int64_t n, slick_value *sources) {
    slick_iter *it = slick_xmalloc(sizeof(*it));
    it->kind = SLICK_ITER_ZIP;
    it->width = (int32_t)n;
    it->source = slick_rt_array(SLICK_ARRAY, n, sources);
    int64_t length = -1;
    for (int64_t i = 0; i < n; i++) {
        slick_value seq = slick_rt_iter_of(sources[i]);
        slick_iter *inner = (slick_iter *)(uintptr_t)seq.bits;
        if (length < 0 || inner->length < length) {
            length = inner->length;
        }
        slick_as_array(it->source)->items[i] = seq;
    }
    it->length = length < 0 ? 0 : length;
    slick_value out = {SLICK_ITERABLE, 0, (int64_t)(uintptr_t)it};
    return out;
}

static slick_iter *slick_as_iter(slick_value v) {
    return (slick_iter *)(uintptr_t)v.bits;
}

int64_t slick_rt_iter_len(slick_value v) {
    return slick_as_iter(slick_rt_iter_of(v))->length;
}

int32_t slick_rt_iter_width(slick_value v) {
    return slick_as_iter(slick_rt_iter_of(v))->width;
}

static slick_value slick_iter_at(slick_value seq, int64_t index, int32_t slot);

slick_value slick_rt_iter_item(slick_value seq, int64_t index) {
    slick_iter *it = slick_as_iter(slick_rt_iter_of(seq));
    if (it->width <= 1) {
        return slick_iter_at(seq, index, 0);
    }
    slick_value *items = slick_xmalloc(sizeof(slick_value) * (size_t)it->width);
    for (int32_t slot = 0; slot < it->width; slot++) {
        items[slot] = slick_iter_at(seq, index, slot);
    }
    return slick_rt_array(SLICK_TUPLE, it->width, items);
}

static slick_value slick_iter_at(slick_value seq, int64_t index, int32_t slot) {
    slick_iter *it = slick_as_iter(slick_rt_iter_of(seq));
    switch (it->kind) {
    case SLICK_ITER_RANGE:
        return slick_rt_int(it->start + index);
    case SLICK_ITER_ARRAY: {
        slick_array *a = slick_as_array(it->source);
        slick_value item = a->items[index];
        if (item.kind == SLICK_TUPLE) {
            slick_array *tuple = slick_as_array(item);
            return slot >= 0 && slot < tuple->len ? tuple->items[slot] : slick_null();
        }
        return item;
    }
    case SLICK_ITER_MAP: {
        slick_map *m = (slick_map *)(uintptr_t)it->source.bits;
        return slot == 0 ? m->entries[index].key : m->entries[index].value;
    }
    case SLICK_ITER_ENUM:
        return slot == 0 ? slick_rt_int(index) : slick_rt_iter_item(it->source, index);
    case SLICK_ITER_ZIP: {
        slick_array *a = slick_as_array(it->source);
        return slick_rt_iter_item(a->items[slot], index);
    }
    default:
        return slick_null();
    }
}

slick_value slick_rt_iter_at(slick_value seq, int64_t index, int32_t slot) {
    return slick_iter_at(seq, index, slot);
}

slick_value slick_rt_field(slick_value obj, int32_t index) {
    slick_class *c = slick_as_class(obj);
    if (!c || index < 0 || index >= c->field_count) {
        return slick_null();
    }
    return c->fields[index];
}

void slick_rt_set_field(slick_value obj, int32_t index, slick_value value) {
    slick_class *c = slick_as_class(obj);
    if (!c || index < 0 || index >= c->field_count) {
        return;
    }
    c->fields[index] = value;
}

slick_value slick_rt_union_field(slick_value obj, int32_t index) {
    slick_union_obj *u = slick_as_union(obj);
    if (!u || index < 0 || index >= u->field_count) {
        return slick_null();
    }
    return u->fields[index];
}

int32_t slick_rt_union_tag(slick_value obj) {
    slick_union_obj *u = slick_as_union(obj);
    return u ? u->tag : 0;
}

int32_t slick_rt_class_type(slick_value obj) {
    slick_class *c = slick_as_class(obj);
    return c ? c->type_id : -1;
}

int32_t slick_rt_is_error(slick_value v) {
    return v.kind == SLICK_ERROR;
}

int32_t slick_rt_optional_present(slick_value v) {
    if (v.kind == SLICK_OPTIONAL) {
        return v.flags != 0;
    }
    return v.kind != SLICK_NULL;
}

slick_value slick_rt_optional_value(slick_value v) {
    if (v.kind == SLICK_OPTIONAL) {
        return slick_payload(v);
    }
    return v;
}

int32_t slick_rt_result_ok(slick_value v) {
    return v.kind == SLICK_RESULT && v.flags != 0;
}

slick_value slick_rt_result_payload(slick_value v) {
    return slick_payload(v);
}

slick_value slick_rt_promote(slick_value v, int32_t optional) {
    if (!optional) {
        return v;
    }
    if (v.kind == SLICK_OPTIONAL) {
        return v;
    }
    if (v.kind == SLICK_NULL) {
        return slick_rt_none();
    }
    return slick_rt_some(v);
}

static int slick_map_key_eq(slick_value a, slick_value b) {
    if (a.kind != b.kind) {
        return 0;
    }
    if (a.kind == SLICK_STRING) {
        if (slick_clen(a) != slick_clen(b)) {
            return 0;
        }
        return memcmp(slick_cstr(a), slick_cstr(b), (size_t)slick_clen(a)) == 0;
    }
    return a.bits == b.bits;
}

slick_value slick_rt_map(int64_t n, slick_value *keys, slick_value *vals) {
    slick_map *m = slick_xmalloc(sizeof(*m));
    for (int64_t i = 0; i < n; i++) {
        int found = -1;
        for (int64_t j = 0; j < m->len; j++) {
            if (slick_map_key_eq(m->entries[j].key, keys[i])) {
                found = (int)j;
                break;
            }
        }
        if (found >= 0) {
            m->entries[found].value = vals[i];
            continue;
        }
        m->entries = slick_xrealloc(m->entries, sizeof(slick_map_entry) * (size_t)(m->len + 1));
        m->entries[m->len].key = keys[i];
        m->entries[m->len].value = vals[i];
        m->len++;
    }
    slick_value out = {SLICK_MAP, 0, (int64_t)(uintptr_t)m};
    return out;
}

static slick_map *slick_as_map(slick_value v) {
    return (slick_map *)(uintptr_t)v.bits;
}

slick_value slick_rt_map_get(slick_value map, slick_value key) {
    slick_map *m = slick_as_map(map);
    for (int64_t i = 0; i < m->len; i++) {
        if (slick_map_key_eq(m->entries[i].key, key)) {
            return slick_rt_some(m->entries[i].value);
        }
    }
    return slick_rt_none();
}

int32_t slick_rt_map_contains(slick_value map, slick_value key) {
    return slick_rt_optional_present(slick_rt_map_get(map, key));
}

slick_value slick_rt_map_with(slick_value map, slick_value key, slick_value val) {
    slick_map *m = slick_as_map(map);
    slick_value *keys = slick_xmalloc(sizeof(slick_value) * (size_t)(m->len + 1));
    slick_value *vals = slick_xmalloc(sizeof(slick_value) * (size_t)(m->len + 1));
    for (int64_t i = 0; i < m->len; i++) {
        keys[i] = m->entries[i].key;
        vals[i] = m->entries[i].value;
    }
    keys[m->len] = key;
    vals[m->len] = val;
    return slick_rt_map(m->len + 1, keys, vals);
}

slick_value slick_rt_map_without(slick_value map, slick_value key) {
    slick_map *m = slick_as_map(map);
    slick_value *keys = slick_xmalloc(sizeof(slick_value) * (size_t)m->len);
    slick_value *vals = slick_xmalloc(sizeof(slick_value) * (size_t)m->len);
    int64_t n = 0;
    for (int64_t i = 0; i < m->len; i++) {
        if (slick_map_key_eq(m->entries[i].key, key)) {
            continue;
        }
        keys[n] = m->entries[i].key;
        vals[n] = m->entries[i].value;
        n++;
    }
    return slick_rt_map(n, keys, vals);
}

int64_t slick_rt_map_len(slick_value map) {
    return slick_as_map(map)->len;
}

slick_value slick_rt_buffer_new(void) {
    slick_buffer *b = slick_xmalloc(sizeof(*b));
    slick_value out = {SLICK_BUFFER, 0, (int64_t)(uintptr_t)b};
    return out;
}

static slick_buffer *slick_as_buf(slick_value v) {
    return (slick_buffer *)(uintptr_t)v.bits;
}

void slick_rt_buffer_push(slick_value buf, slick_value val) {
    slick_buffer *b = slick_as_buf(buf);
    if (b->len + 1 > b->cap) {
        b->cap = b->cap ? b->cap * 2 : 4;
        b->items = slick_xrealloc(b->items, sizeof(slick_value) * (size_t)b->cap);
    }
    b->items[b->len++] = val;
}

slick_value slick_rt_buffer_get(slick_value buf, int64_t index) {
    slick_buffer *b = slick_as_buf(buf);
    if (index < 0 || index >= b->len) {
        return slick_rt_none();
    }
    slick_value item = b->items[index];
    if (item.kind == SLICK_OPTIONAL) {
        return item;
    }
    return slick_rt_some(item);
}

slick_value slick_rt_buffer_set(slick_value buf, int64_t index, slick_value val, int32_t fail_type) {
    slick_buffer *b = slick_as_buf(buf);
    if (index < 0 || index >= b->len) {
        return slick_rt_result(0, slick_rt_class(fail_type, 0, NULL));
    }
    b->items[index] = val;
    return slick_rt_result(1, slick_null());
}

int64_t slick_rt_buffer_len(slick_value buf) {
    return slick_as_buf(buf)->len;
}

slick_value slick_rt_buffer_freeze(slick_value buf) {
    slick_buffer *b = slick_as_buf(buf);
    return slick_rt_array(SLICK_ARRAY, b->len, b->items);
}

int64_t slick_rt_array_len(slick_value a) {
    return slick_as_array(a)->len;
}

slick_value slick_rt_array_get(slick_value a, int64_t index) {
    slick_array *arr = slick_as_array(a);
    if (index < 0 || index >= arr->len) {
        return slick_rt_none();
    }
    slick_value item = arr->items[index];
    if (item.kind == SLICK_OPTIONAL) {
        return item;
    }
    return slick_rt_some(item);
}

slick_value slick_rt_array_index(slick_value a, int64_t index) {
    return slick_as_array(a)->items[index];
}

slick_value slick_rt_array_slice(slick_value a, int64_t start, int64_t end, int32_t fail_type) {
    slick_array *arr = slick_as_array(a);
    if (start < 0 || end < start || end > arr->len) {
        return slick_rt_result(0, slick_rt_class(fail_type, 0, NULL));
    }
    return slick_rt_result(1, slick_rt_array(SLICK_ARRAY, end - start, arr->items + start));
}

static int slick_equal_impl(slick_value a, slick_value b);

int32_t slick_rt_equal(slick_value a, slick_value b) {
    return slick_equal_impl(a, b);
}

static int slick_equal_impl(slick_value a, slick_value b) {
    if (a.kind == SLICK_OPTIONAL || b.kind == SLICK_OPTIONAL || a.kind == SLICK_NULL || b.kind == SLICK_NULL) {
        int ap = slick_rt_optional_present(a);
        int bp = slick_rt_optional_present(b);
        if (!ap && !bp) {
            return 1;
        }
        if (ap != bp) {
            return 0;
        }
        return slick_equal_impl(slick_rt_optional_value(a), slick_rt_optional_value(b));
    }
    if (a.kind != b.kind) {
        return 0;
    }
    switch (a.kind) {
    case SLICK_BOOL:
    case SLICK_INT:
        return a.bits == b.bits;
    case SLICK_FLOAT: {
        double x = slick_as_float(a), y = slick_as_float(b);
        return x == y;
    }
    case SLICK_STRING:
    case SLICK_BYTES: {
        if (slick_clen(a) != slick_clen(b)) {
            return 0;
        }
        return memcmp(slick_cstr(a), slick_cstr(b), (size_t)slick_clen(a)) == 0;
    }
    case SLICK_ARRAY:
    case SLICK_TUPLE: {
        slick_array *l = slick_as_array(a), *r = slick_as_array(b);
        if (l->len != r->len) {
            return 0;
        }
        for (int64_t i = 0; i < l->len; i++) {
            if (!slick_equal_impl(l->items[i], r->items[i])) {
                return 0;
            }
        }
        return 1;
    }
    case SLICK_MAP: {
        slick_map *l = slick_as_map(a), *r = slick_as_map(b);
        if (l->len != r->len) {
            return 0;
        }
        for (int64_t i = 0; i < l->len; i++) {
            if (!slick_equal_impl(l->entries[i].key, r->entries[i].key) ||
                !slick_equal_impl(l->entries[i].value, r->entries[i].value)) {
                return 0;
            }
        }
        return 1;
    }
    case SLICK_RESULT:
        return a.flags == b.flags && slick_equal_impl(slick_payload(a), slick_payload(b));
    case SLICK_UNION: {
        slick_union_obj *l = slick_as_union(a), *r = slick_as_union(b);
        if (!l || !r || l->type_id != r->type_id || l->tag != r->tag || l->field_count != r->field_count) {
            return 0;
        }
        for (int32_t i = 0; i < l->field_count; i++) {
            if (!slick_equal_impl(l->fields[i], r->fields[i])) {
                return 0;
            }
        }
        return 1;
    }
    case SLICK_CLASS:
    case SLICK_ERROR: {
        slick_class *l = slick_as_class(a), *r = slick_as_class(b);
        if (!l || !r || l->type_id != r->type_id || l->field_count != r->field_count) {
            return 0;
        }
        if (l->resource || r->resource) {
            return l->resource && r->resource && l->resource == r->resource;
        }
        for (int32_t i = 0; i < l->field_count; i++) {
            if (!slick_equal_impl(l->fields[i], r->fields[i])) {
                return 0;
            }
        }
        return 1;
    }
    case SLICK_CALLABLE:
        return 0;
    default:
        return a.bits == b.bits;
    }
}

static void slick_format_into(char **out, size_t *len, size_t *cap, slick_value v);
slick_value slick_rt_format_union_value(slick_value v);


static void slick_append(char **out, size_t *len, size_t *cap, const char *s, size_t n) {
    if (*len + n + 1 > *cap) {
        *cap = (*cap + n + 64) * 2;
        *out = slick_xrealloc(*out, *cap);
    }
    memcpy(*out + *len, s, n);
    *len += n;
    (*out)[*len] = 0;
}

static void slick_append_str(char **out, size_t *len, size_t *cap, const char *s) {
    slick_append(out, len, cap, s, strlen(s));
}

static int slick_decimal_exponent(const char *text) {
    const char *start = text + (*text == '-' || *text == '+');
    const char *exponent = strchr(start, 'e');
    if (exponent) return atoi(exponent + 1);
    const char *point = strchr(start, '.');
    const char *digit = start;
    while (*digit == '0' || *digit == '.') digit++;
    if (!*digit) return 0;
    if (!point) return (int)strlen(start) - 1;
    if (digit < point) return (int)(point - digit) - 1;
    return -(int)(digit - point);
}

static void slick_format_float_c(char *out, size_t capacity, double value) {
    if (isnan(value)) {
        snprintf(out, capacity, "NaN");
        return;
    }
    if (isinf(value)) {
        snprintf(out, capacity, signbit(value) ? "-Inf" : "+Inf");
        return;
    }
    uint64_t target;
    memcpy(&target, &value, sizeof(target));
    for (int precision = 1; precision <= 17; precision++) {
        char candidate[64];
        snprintf(candidate, sizeof(candidate), "%.*g", precision, value);
        char *end = NULL;
        double parsed = strtod(candidate, &end);
        uint64_t bits;
        memcpy(&bits, &parsed, sizeof(bits));
        if (end && *end == 0 && bits == target) {
            int exponent = slick_decimal_exponent(candidate);
            if (exponent >= -4 && exponent < 6) {
                int places = precision - exponent - 1;
                snprintf(out, capacity, "%.*f", places > 0 ? places : 0, value);
            } else {
                snprintf(out, capacity, "%.*e", precision - 1, value);
            }
            return;
        }
    }
    snprintf(out, capacity, "%.17g", value);
}

static locale_t slick_numeric_locale;
static pthread_once_t slick_numeric_locale_once = PTHREAD_ONCE_INIT;

static void slick_init_numeric_locale(void) {
    slick_numeric_locale = newlocale(LC_NUMERIC_MASK, "C", (locale_t)0);
}

static void slick_format_float(char *out, size_t capacity, double value) {
    pthread_once(&slick_numeric_locale_once, slick_init_numeric_locale);
    locale_t previous = (locale_t)0;
    if (slick_numeric_locale) previous = uselocale(slick_numeric_locale);
    slick_format_float_c(out, capacity, value);
    if (slick_numeric_locale) uselocale(previous);
}

slick_value slick_rt_float_text(double value) {
    char out[64];
    slick_format_float(out, sizeof(out), value);
    return slick_rt_string(out, -1);
}

static void slick_format_into(char **out, size_t *len, size_t *cap, slick_value v) {
    char buf[64];
    if (v.kind == SLICK_OPTIONAL) {
        if (!v.flags) {
            return;
        }
        slick_format_into(out, len, cap, slick_payload(v));
        return;
    }
    switch (v.kind) {
    case SLICK_NULL:
        return;
    case SLICK_BOOL:
        slick_append_str(out, len, cap, v.bits ? "true" : "false");
        return;
    case SLICK_INT:
        snprintf(buf, sizeof(buf), "%" PRId64, v.bits);
        slick_append_str(out, len, cap, buf);
        return;
    case SLICK_FLOAT: {
        double d;
        memcpy(&d, &v.bits, sizeof(double));
        slick_format_float(buf, sizeof(buf), d);
        slick_append_str(out, len, cap, buf);
        return;
    }
    case SLICK_STRING:
        slick_append(out, len, cap, slick_cstr(v), (size_t)slick_clen(v));
        return;
    case SLICK_BYTES:
        snprintf(buf, sizeof(buf), "bytes[%" PRId64 "]", slick_clen(v));
        slick_append_str(out, len, cap, buf);
        return;
    case SLICK_BUFFER:
        slick_append_str(out, len, cap, "Buffer");
        return;
    case SLICK_CALLABLE:
        slick_append_str(out, len, cap, "<callable>");
        return;
    case SLICK_ARRAY:
    case SLICK_TUPLE: {
        slick_array *a = slick_as_array(v);
        slick_append_str(out, len, cap, v.kind == SLICK_TUPLE ? "(" : "[");
        for (int64_t i = 0; i < a->len; i++) {
            if (i) {
                slick_append_str(out, len, cap, ", ");
            }
            slick_format_into(out, len, cap, a->items[i]);
        }
        slick_append_str(out, len, cap, v.kind == SLICK_TUPLE ? ")" : "]");
        return;
    }
    case SLICK_MAP: {
        slick_map *m = slick_as_map(v);
        slick_append_str(out, len, cap, "map {");
        for (int64_t i = 0; i < m->len; i++) {
            if (i) {
                slick_append_str(out, len, cap, ", ");
            }
            slick_format_into(out, len, cap, m->entries[i].key);
            slick_append_str(out, len, cap, ": ");
            slick_format_into(out, len, cap, m->entries[i].value);
        }
        slick_append_str(out, len, cap, "}");
        return;
    }
    case SLICK_RESULT:
        slick_append_str(out, len, cap, v.flags ? "Ok(" : "Err(");
        slick_format_into(out, len, cap, slick_payload(v));
        slick_append_str(out, len, cap, ")");
        return;
    case SLICK_CLASS:
    case SLICK_ERROR:
        if (slick_as_class(v) && slick_as_class(v)->type_id >= 0 && slick_as_class(v)->type_id < slick_type_count) {
            slick_append_str(out, len, cap, slick_types[slick_as_class(v)->type_id].name);
        }
        return;
    case SLICK_UNION: {
        slick_value formatted = slick_rt_format_union_value(v);
        slick_append(out, len, cap, slick_cstr(formatted), (size_t)slick_clen(formatted));
        return;
    }
    case SLICK_ITERABLE: {
        slick_iter *it = slick_as_iter(v);
        slick_append_str(out, len, cap, "[");
        for (int64_t i = 0; i < it->length; i++) {
            if (i) {
                slick_append_str(out, len, cap, ", ");
            }
            slick_format_into(out, len, cap, slick_rt_iter_item(v, i));
        }
        slick_append_str(out, len, cap, "]");
        return;
    }
    default:
        return;
    }
}

slick_value slick_rt_format(slick_value v) {
    char *out = NULL;
    size_t len = 0, cap = 0;
    slick_format_into(&out, &len, &cap, v);
    if (!out) {
        return slick_rt_string("", 0);
    }
    slick_value s = slick_rt_string(out, (int64_t)len);
    slick_xfree(out);
    return s;
}

slick_value slick_rt_format_union(const char *name, int32_t n, slick_value *fields) {
    char *out = NULL;
    size_t len = 0, cap = 0;
    slick_append_str(&out, &len, &cap, name);
    if (n > 0) {
        slick_append_str(&out, &len, &cap, "(");
        for (int32_t i = 0; i < n; i++) {
            if (i) {
                slick_append_str(&out, &len, &cap, ", ");
            }
            slick_format_into(&out, &len, &cap, fields[i]);
        }
        slick_append_str(&out, &len, &cap, ")");
    }
    slick_value s = slick_rt_string(out ? out : "", (int64_t)len);
    slick_xfree(out);
    return s;
}

static slick_value slick_error_class(int32_t type_id, slick_value message) {
    const slick_type_info *info = (type_id >= 0 && type_id < slick_type_count) ? &slick_types[type_id] : NULL;
    int n = info ? info->field_count : 0;
    slick_value *fields = n ? slick_xmalloc(sizeof(slick_value) * (size_t)n) : NULL;
    for (int i = 0; i < n; i++) {
        if (info && info->field_names && strcmp(info->field_names[i], "Message") == 0) {
            fields[i] = message;
        } else {
            fields[i] = slick_null();
        }
    }
    slick_value value = slick_rt_class(type_id, n, fields);
    slick_class *object = slick_as_class(value);
    if (object) object->error_message = message;
    return value;
}

slick_value slick_rt_error_message(int32_t type_id, slick_value message) {
    return slick_error_class(type_id, message);
}

static int slick_context_cancelled(slick_ctx *ctx) {
    for (; ctx; ctx = ctx->parent) {
        if (__atomic_load_n(&ctx->cancelled, __ATOMIC_ACQUIRE)) return 1;
    }
    return 0;
}

slick_outcome slick_rt_check_cancel(slick_ctx *ctx) {
    if (slick_context_cancelled(ctx)) {
        slick_outcome o;
        o.code = SLICK_CANCEL;
        o.pad = 0;
        o.value = slick_rt_string("task cancelled", -1);
        return o;
    }
    return slick_ok(slick_null());
}

int32_t slick_rt_is_control(int32_t code) {
    return code == SLICK_RETURN || code == SLICK_BREAK || code == SLICK_CONTINUE;
}

slick_value slick_rt_error_format(slick_value value);

slick_value slick_rt_suppress(slick_value primary, slick_value extra) {
    if (primary.kind == SLICK_ERROR || primary.kind == SLICK_CLASS) {
        slick_class *object = slick_as_class(primary);
        if (object) {
            slick_class *copy = slick_xmalloc(sizeof(*copy));
            *copy = *object;
            copy->suppressed_count = object->suppressed_count + 1;
            copy->suppressed_capacity = copy->suppressed_count;
            copy->suppressed = slick_xmalloc(
                sizeof(*copy->suppressed) * (size_t)copy->suppressed_count);
            if (object->suppressed_count > 0) {
                memcpy(copy->suppressed, object->suppressed,
                    sizeof(*copy->suppressed) * (size_t)object->suppressed_count);
            }
            copy->suppressed[object->suppressed_count] = extra;
            primary.bits = (int64_t)(uintptr_t)copy;
            return primary;
        }
    }
    slick_value text = slick_rt_error_format(primary);
    slick_value more = slick_rt_error_format(extra);
    char *out = NULL;
    size_t len = 0, cap = 0;
    slick_append_str(&out, &len, &cap, slick_cstr(text));
    slick_append_str(&out, &len, &cap, " (suppressed: ");
    slick_append_str(&out, &len, &cap, slick_cstr(more));
    slick_append_str(&out, &len, &cap, ")");
    slick_value value = slick_rt_string(out, (int64_t)len);
    slick_xfree(out);
    return value;
}

static void slick_error_format_into(char **out, size_t *len, size_t *cap, slick_value value) {
    if (value.kind != SLICK_ERROR && value.kind != SLICK_CLASS) {
        slick_format_into(out, len, cap, value);
        return;
    }
    slick_class *object = slick_as_class(value);
    if (!object || object->type_id < 0 || object->type_id >= slick_type_count) {
        slick_format_into(out, len, cap, value);
        return;
    }
    const slick_type_info *info = &slick_types[object->type_id];
    slick_append_str(out, len, cap, info->name);
    slick_value message = object->error_message;
    for (int32_t i = 0; info->field_names && i < object->field_count; i++) {
        if (strcmp(info->field_names[i], "Message") == 0 && object->fields[i].kind == SLICK_STRING &&
            slick_as_bytes(object->fields[i])->len > 0) {
            message = object->fields[i];
            break;
        }
    }
    if (message.kind == SLICK_STRING && slick_as_bytes(message)->len > 0) {
        slick_append_str(out, len, cap, ": ");
        slick_append(out, len, cap, (const char *)slick_as_bytes(message)->data,
            (size_t)slick_as_bytes(message)->len);
    }
    if (object->suppressed_count > 0) {
        slick_append_str(out, len, cap, " (suppressed: ");
        for (int32_t i = 0; i < object->suppressed_count; i++) {
            if (i) slick_append_str(out, len, cap, "; ");
            slick_error_format_into(out, len, cap, object->suppressed[i]);
        }
        slick_append_str(out, len, cap, ")");
    }
}

slick_value slick_rt_error_format(slick_value value) {
    char *out = NULL;
    size_t len = 0, cap = 0;
    slick_error_format_into(&out, &len, &cap, value);
    slick_value result = slick_rt_string(out ? out : "", (int64_t)len);
    slick_xfree(out);
    return result;
}

slick_scope *slick_rt_scope_new(slick_ctx *parent) {
    slick_scope *s = slick_xmalloc(sizeof(*s));
    s->parent = parent;
    pthread_mutex_init(&s->mu, NULL);
    return s;
}

static void *slick_task_main(void *arg) {
    slick_task *t = arg;
    slick_current_arena = t->arena;
    t->fn(&t->result, &t->ctx, t->args);
    pthread_mutex_lock(&t->mu);
    t->done = 1;
    pthread_cond_broadcast(&t->cv);
    pthread_mutex_unlock(&t->mu);
    slick_current_arena = NULL;
    return NULL;
}

slick_value slick_rt_task_start(slick_ctx *ctx, slick_scope *scope, slick_task_fn fn, int32_t argc, slick_value *args) {
    slick_task *t = slick_xmalloc(sizeof(*t));
    t->fn = fn;
    t->argc = argc;
    if (argc > 0) {
        t->args = slick_xmalloc(sizeof(slick_value) * (size_t)argc);
        memcpy(t->args, args, sizeof(slick_value) * (size_t)argc);
    }
    t->ctx.cancelled = 0;
    t->ctx.scope = NULL;
    t->ctx.parent = ctx;
    t->parent_arena = slick_active_arena();
    t->arena = slick_rt_arena_push();
    slick_current_arena = t->parent_arena;
    pthread_mutex_init(&t->mu, NULL);
    pthread_cond_init(&t->cv, NULL);
    pthread_mutex_lock(&scope->mu);
    t->next = scope->children;
    scope->children = t;
    pthread_mutex_unlock(&scope->mu);
    int create_error = pthread_create(&t->thread, NULL, slick_task_main, t);
    if (create_error) {
        t->result = slick_throw_val(slick_rt_string(strerror(create_error), -1));
        t->done = 1;
    } else {
        t->started = 1;
    }
    slick_value out = {SLICK_CLASS, 0, (int64_t)(uintptr_t)t};
    return out;
}

static void slick_task_wait(slick_task *t) {
    pthread_mutex_lock(&t->mu);
    while (!t->done) {
        pthread_cond_wait(&t->cv, &t->mu);
    }
    pthread_mutex_unlock(&t->mu);
    if (t->started) {
        pthread_join(t->thread, NULL);
        t->started = 0;
    }
    if (!t->arena_joined) {
        slick_arena_merge(t->parent_arena, t->arena);
        pthread_mutex_destroy(&t->arena->mutex);
        free(t->arena);
        t->arena_joined = 1;
    }
}

slick_outcome slick_rt_task_await(slick_value task) {
    slick_task *t = (slick_task *)(uintptr_t)task.bits;
    if (t->consumed) {
        return slick_throw_val(slick_rt_string("pending binding already awaited", -1));
    }
    t->consumed = 1;
    slick_task_wait(t);
    return t->result;
}

slick_outcome slick_rt_scope_finish(slick_scope *scope, slick_outcome *primary_value) {
    slick_outcome primary = *primary_value;
    int outstanding = 0;
    pthread_mutex_lock(&scope->mu);
    for (slick_task *t = scope->children; t; t = t->next) {
        if (!t->consumed) {
            outstanding = 1;
            __atomic_store_n(&t->ctx.cancelled, 1, __ATOMIC_RELEASE);
        }
    }
    pthread_mutex_unlock(&scope->mu);
    (void)outstanding;
    for (slick_task *t = scope->children; t; t = t->next) {
        if (t->consumed) {
            continue;
        }
        t->consumed = 1;
        slick_task_wait(t);
        if (t->result.code == SLICK_OK || t->result.code == SLICK_CANCEL) {
            continue;
        }
        if (primary.code == SLICK_OK) {
            primary = t->result;
        } else {
            primary.value = slick_rt_suppress(primary.value, t->result.value);
        }
    }
    for (slick_task *t = scope->children; t; t = t->next) {
        pthread_mutex_destroy(&t->mu);
        pthread_cond_destroy(&t->cv);
    }
    pthread_mutex_destroy(&scope->mu);
    return primary;
}

slick_outcome slick_rt_invoke(slick_ctx *ctx, slick_value callee, int32_t argc, slick_value *args) {
    slick_outcome cancel = slick_rt_check_cancel(ctx);
    if (cancel.code != SLICK_OK) {
        return cancel;
    }
    if (callee.kind == SLICK_INTERFACE) {
        slick_iface *i = slick_as_iface(callee);
        slick_value *full = slick_xmalloc(sizeof(slick_value) * (size_t)(argc + 1));
        full[0] = i->receiver;
        if (argc) {
            memcpy(full + 1, args, sizeof(slick_value) * (size_t)argc);
        }
        return i->vtable[0](ctx, full);
    }
    slick_callable *c = slick_as_callable(callee);
    if (!c || !c->code) {
        return slick_throw_val(slick_rt_string("call target is not callable", -1));
    }
    int32_t n = c->capture_count + argc;
    slick_value *full = slick_xmalloc(sizeof(slick_value) * (size_t)(n ? n : 1));
    if (c->capture_count) {
        memcpy(full, c->captures, sizeof(slick_value) * (size_t)c->capture_count);
    }
    if (argc) {
        memcpy(full + c->capture_count, args, sizeof(slick_value) * (size_t)argc);
    }
    return c->code(ctx, full);
}

slick_outcome slick_rt_iface_call(slick_ctx *ctx, slick_value iface, int32_t slot, int32_t argc, slick_value *args) {
    slick_outcome cancel = slick_rt_check_cancel(ctx);
    if (cancel.code != SLICK_OK) {
        return cancel;
    }
    slick_iface *i = slick_as_iface(iface);
    slick_value *full = slick_xmalloc(sizeof(slick_value) * (size_t)(argc + 1));
    full[0] = i->receiver;
    if (argc) {
        memcpy(full + 1, args, sizeof(slick_value) * (size_t)argc);
    }
    return i->vtable[slot](ctx, full);
}

void slick_rt_register_types(const slick_type_info *types, int32_t n) {
    slick_types = types;
    slick_type_count = n;
}

int32_t slick_rt_find_field(int32_t type_id, const char *name) {
    if (type_id < 0 || type_id >= slick_type_count) {
        return -1;
    }
    const slick_type_info *info = &slick_types[type_id];
    for (int32_t i = 0; i < info->field_count; i++) {
        if (strcmp(info->field_names[i], name) == 0) {
            return i;
        }
    }
    return -1;
}


/* --- arithmetic / unary helpers used by generated IR --- */
static int64_t slick_wrapped_int(uint64_t bits) {
    int64_t value;
    memcpy(&value, &bits, sizeof(value));
    return value;
}


slick_value slick_rt_neg(slick_value v) {
    if (v.kind == SLICK_FLOAT) {
        return slick_rt_float(-slick_as_float(v));
    }
    return slick_rt_int(slick_wrapped_int(0 - (uint64_t)v.bits));
}

slick_value slick_rt_not(slick_value v) {
    return slick_rt_bool(!v.bits);
}

slick_value slick_rt_add(slick_value a, slick_value b) {
    if (a.kind == SLICK_STRING || b.kind == SLICK_STRING) {
        slick_value fa = slick_rt_format(a);
        slick_value fb = slick_rt_format(b);
        int64_t n = slick_clen(fa) + slick_clen(fb);
        char *p = slick_xmalloc((size_t)n + 1);
        memcpy(p, slick_cstr(fa), (size_t)slick_clen(fa));
        memcpy(p + slick_clen(fa), slick_cstr(fb), (size_t)slick_clen(fb));
        return slick_rt_string(p, n);
    }
    if (a.kind == SLICK_FLOAT || b.kind == SLICK_FLOAT) {
        double x = a.kind == SLICK_FLOAT ? slick_as_float(a) : (double)a.bits;
        double y = b.kind == SLICK_FLOAT ? slick_as_float(b) : (double)b.bits;
        return slick_rt_float(x + y);
    }
    return slick_rt_int(slick_wrapped_int((uint64_t)a.bits + (uint64_t)b.bits));
}

slick_value slick_rt_sub(slick_value a, slick_value b) {
    if (a.kind == SLICK_FLOAT || b.kind == SLICK_FLOAT) {
        double x = a.kind == SLICK_FLOAT ? slick_as_float(a) : (double)a.bits;
        double y = b.kind == SLICK_FLOAT ? slick_as_float(b) : (double)b.bits;
        return slick_rt_float(x - y);
    }
    return slick_rt_int(slick_wrapped_int((uint64_t)a.bits - (uint64_t)b.bits));
}

slick_value slick_rt_mul(slick_value a, slick_value b) {
    if (a.kind == SLICK_FLOAT || b.kind == SLICK_FLOAT) {
        double x = a.kind == SLICK_FLOAT ? slick_as_float(a) : (double)a.bits;
        double y = b.kind == SLICK_FLOAT ? slick_as_float(b) : (double)b.bits;
        return slick_rt_float(x * y);
    }
    return slick_rt_int(slick_wrapped_int((uint64_t)a.bits * (uint64_t)b.bits));
}

slick_value slick_rt_cmp(slick_value a, slick_value b, int32_t op) {
    /* op: 0 <, 1 <=, 2 >, 3 >= */
    if (a.kind == SLICK_STRING) {
        int c = strcmp(slick_cstr(a), slick_cstr(b));
        switch (op) {
        case 0: return slick_rt_bool(c < 0);
        case 1: return slick_rt_bool(c <= 0);
        case 2: return slick_rt_bool(c > 0);
        default: return slick_rt_bool(c >= 0);
        }
    }
    if (a.kind == SLICK_FLOAT || b.kind == SLICK_FLOAT) {
        double x = a.kind == SLICK_FLOAT ? slick_as_float(a) : (double)a.bits;
        double y = b.kind == SLICK_FLOAT ? slick_as_float(b) : (double)b.bits;
        switch (op) {
        case 0: return slick_rt_bool(x < y);
        case 1: return slick_rt_bool(x <= y);
        case 2: return slick_rt_bool(x > y);
        default: return slick_rt_bool(x >= y);
        }
    }
    switch (op) {
    case 0: return slick_rt_bool(a.bits < b.bits);
    case 1: return slick_rt_bool(a.bits <= b.bits);
    case 2: return slick_rt_bool(a.bits > b.bits);
    default: return slick_rt_bool(a.bits >= b.bits);
    }
}

int32_t slick_rt_truth(slick_value v) {
    return v.bits != 0;
}

static slick_ctx slick_root_context;
static pthread_once_t slick_signal_once = PTHREAD_ONCE_INIT;

static void slick_cancel_signal(int signal_number) {
    (void)signal_number;
    __atomic_store_n(&slick_root_context.cancelled, 1, __ATOMIC_RELEASE);
}

static void slick_install_signal_handlers(void) {
    struct sigaction action = {0};
    action.sa_handler = slick_cancel_signal;
    sigemptyset(&action.sa_mask);
    sigaction(SIGINT, &action, NULL);
    sigaction(SIGTERM, &action, NULL);
}

slick_ctx *slick_rt_root_ctx(void) {
    pthread_once(&slick_signal_once, slick_install_signal_handlers);
    return &slick_root_context;
}
slick_ctx *slick_rt_cleanup_ctx(void) {
    static _Thread_local slick_ctx cleanup;
    cleanup.cancelled = 0;
    cleanup.scope = NULL;
    cleanup.parent = NULL;
    return &cleanup;
}


void slick_rt_print(slick_value v) {
    slick_value s = slick_rt_format(v);
    if (slick_clen(s) == 0) {
        return;
    }
    fwrite(slick_cstr(s), 1, (size_t)slick_clen(s), stdout);
    fputc('\n', stdout);
}

void slick_rt_write_bytes(slick_value v, int fd) {
    slick_bytes *b = slick_as_bytes(v);
    if (!b || !b->data) {
        return;
    }
    FILE *f = fd == 2 ? stderr : stdout;
    fwrite(b->data, 1, (size_t)b->len, f);
}
void slick_rt_invalid_exit(int64_t code) {
    fprintf(stderr, "std.process.Status ExitCode must be 0 through 255, found %" PRId64 "\n", code);
}

slick_value slick_rt_argv(int argc, char **argv) {
    slick_value *items = slick_xmalloc(sizeof(slick_value) * (size_t)(argc > 0 ? argc : 1));
    for (int i = 0; i < argc; i++) {
        items[i] = slick_rt_string(argv[i], -1);
    }
    return slick_rt_array(SLICK_ARRAY, argc, items);
}

void slick_rt_abort_missing(const char *what) {
    fprintf(stderr, "LLVM backend missing lowering: %s\n", what);
    abort();
}

int32_t slick_rt_type_id(const char *name) {
    for (int i = 0; i < slick_type_count; i++) {
        if (strcmp(slick_types[i].name, name) == 0) {
            return i;
        }
    }
    return -1;
}

typedef struct slick_union_info {
    const char *name;
    int32_t variant_count;
    const char **variant_names;
    int32_t *field_counts;
} slick_union_info;

static const slick_union_info *slick_unions;
static int slick_union_count;

void slick_rt_register_unions(const slick_union_info *unions, int32_t n) {
    slick_unions = unions;
    slick_union_count = n;
}

int32_t slick_rt_union_id(const char *name) {
    for (int i = 0; i < slick_union_count; i++) {
        if (strcmp(slick_unions[i].name, name) == 0) {
            return i;
        }
    }
    return -1;
}

slick_value slick_rt_format_union_value(slick_value v) {
    slick_union_obj *u = slick_as_union(v);
    if (!u || u->type_id < 0 || u->type_id >= slick_union_count) {
        return slick_rt_string("", 0);
    }
    const slick_union_info *info = &slick_unions[u->type_id];
    const char *name = "";
    if (u->tag > 0 && u->tag <= info->variant_count) {
        name = info->variant_names[u->tag - 1];
    }
    return slick_rt_format_union(name, u->field_count, u->fields);
}

static slick_type_info *slick_type_store;
static slick_union_info *slick_union_store;

void slick_rt_set_type_count(int32_t n) {
    slick_type_store = slick_xmalloc(sizeof(slick_type_info) * (size_t)(n > 0 ? n : 1));
    slick_types = slick_type_store;
    slick_type_count = n;
}

void slick_rt_set_type(int32_t id, const char *name, int32_t nfields, const char **names,
        const char **json_names, const char **schemas, int32_t is_error, int32_t native) {
    slick_type_store[id].name = name;
    slick_type_store[id].field_count = nfields;
    slick_type_store[id].field_names = names;
    slick_type_store[id].json_names = json_names;
    slick_type_store[id].field_schemas = schemas;
    slick_type_store[id].is_error = is_error;
    slick_type_store[id].native_resource = native;
}

void slick_rt_set_union_count(int32_t n) {
    slick_union_store = slick_xmalloc(sizeof(slick_union_info) * (size_t)(n > 0 ? n : 1));
    slick_unions = slick_union_store;
    slick_union_count = n;
}

void slick_rt_set_union(int32_t id, const char *name, int32_t nvars, const char **names, int32_t *counts) {
    slick_union_store[id].name = name;
    slick_union_store[id].variant_count = nvars;
    slick_union_store[id].variant_names = names;
    slick_union_store[id].field_counts = counts;
}

slick_value slick_rt_empty_map(void) {
    return slick_rt_map(0, NULL, NULL);
}

slick_value slick_rt_string_cstr(const char *s) {
    return slick_rt_string(s, -1);
}

int32_t slick_rt_type_field_count(int32_t id) {
    if (id < 0 || id >= slick_type_count) {
        return 0;
    }
    return slick_types[id].field_count;
}

const char *slick_rt_type_field_name(int32_t id, int32_t i) {
    if (id < 0 || id >= slick_type_count || i < 0 || i >= slick_types[id].field_count) {
        return "";
    }
    return slick_types[id].field_names[i];
}

#ifdef SLICK_HAS_JSON
typedef struct slick_json_error {
    char *path;
    const char *message;
} slick_json_error;

static char *slick_json_field_path(const char *path, const char *field) {
    size_t path_length = strlen(path);
    size_t field_length = strlen(field);
    char *out = slick_xmalloc(path_length + field_length + 2);
    memcpy(out, path, path_length);
    out[path_length] = '.';
    memcpy(out + path_length + 1, field, field_length + 1);
    return out;
}

static char *slick_json_index_path(const char *path, size_t index) {
    char suffix[32];
    int suffix_length = snprintf(suffix, sizeof(suffix), "[%zu]", index);
    size_t path_length = strlen(path);
    char *out = slick_xmalloc(path_length + (size_t)suffix_length + 1);
    memcpy(out, path, path_length);
    memcpy(out + path_length, suffix, (size_t)suffix_length + 1);
    return out;
}

static int32_t slick_json_failure_type(void) {
    for (int32_t i = 0; i < slick_type_count; i++) {
        if (strcmp(slick_types[i].name, "std.json.Failure") == 0) return i;
    }
    return -1;
}

static slick_value slick_json_failure(const char *operation, const char *path, const char *message) {
    int32_t id = slick_json_failure_type();
    if (id < 0) return slick_rt_string(message, -1);
    const slick_type_info *info = &slick_types[id];
    slick_value *fields = slick_xmalloc(sizeof(*fields) * (size_t)info->field_count);
    for (int32_t i = 0; i < info->field_count; i++) {
        const char *name = info->field_names[i];
        fields[i] = slick_rt_string(
            strcmp(name, "Operation") == 0 ? operation :
            strcmp(name, "Path") == 0 ? path :
            strcmp(name, "Message") == 0 ? message : "", -1);
    }
    return slick_rt_class(id, info->field_count, fields);
}

static int32_t slick_json_schema_class(const char **schema) {
    char *end = NULL;
    long id = strtol(*schema, &end, 10);
    if (end == *schema || *end != ';' || id < 0 || id >= slick_type_count) return -1;
    *schema = end + 1;
    return (int32_t)id;
}

static int slick_json_decode_value(json_t *node, const char **schema, const char *path,
        slick_value *out, slick_json_error *error) {
    char kind = *(*schema)++;
    if (kind == '?') {
        if (json_is_null(node)) {
            *out = slick_rt_none();
            return 1;
        }
        slick_value value;
        if (!slick_json_decode_value(node, schema, path, &value, error)) return 0;
        *out = slick_rt_some(value);
        return 1;
    }
    if (kind == '[') {
        if (!json_is_array(node)) {
            error->path = slick_xstrdup(path); error->message = "expected JSON array"; return 0;
        }
        const char *element_schema = *schema;
        size_t length = json_array_size(node);
        slick_value *items = slick_xmalloc(sizeof(*items) * (length ? length : 1));
        for (size_t i = 0; i < length; i++) {
            const char *element = element_schema;
            char *item_path = slick_json_index_path(path, i);
            if (!slick_json_decode_value(json_array_get(node, i), &element, item_path, &items[i], error)) return 0;
        }
        *out = slick_rt_array(SLICK_ARRAY, (int64_t)length, items);
        return 1;
    }
    if (kind == 'n') {
        if (!json_is_null(node)) { error->path = slick_xstrdup(path); error->message = "expected JSON null"; return 0; }
        *out = slick_null(); return 1;
    }
    if (kind == 'b') {
        if (!json_is_boolean(node)) { error->path = slick_xstrdup(path); error->message = "expected JSON boolean"; return 0; }
        *out = slick_rt_bool(json_is_true(node)); return 1;
    }
    if (kind == 's') {
        if (!json_is_string(node)) { error->path = slick_xstrdup(path); error->message = "expected JSON string"; return 0; }
        *out = slick_rt_string(json_string_value(node), (int64_t)json_string_length(node)); return 1;
    }
    if (kind == 'i') {
        if (json_is_real(node)) {
            error->path = slick_xstrdup(path); error->message = "expected JSON integer without fraction or exponent"; return 0;
        }
        if (!json_is_integer(node)) { error->path = slick_xstrdup(path); error->message = "expected JSON integer"; return 0; }
        *out = slick_rt_int((int64_t)json_integer_value(node)); return 1;
    }
    if (kind == 'f') {
        if (!json_is_number(node)) { error->path = slick_xstrdup(path); error->message = "expected JSON number"; return 0; }
        double number = json_number_value(node);
        if (!isfinite(number)) { error->path = slick_xstrdup(path); error->message = "number out of float64 range"; return 0; }
        *out = slick_rt_float(number); return 1;
    }
    if (kind == 'c') {
        int32_t id = slick_json_schema_class(schema);
        if (id < 0) { error->path = slick_xstrdup(path); error->message = "unsupported JSON target type"; return 0; }
        if (!json_is_object(node)) { error->path = slick_xstrdup(path); error->message = "expected JSON object"; return 0; }
        const slick_type_info *info = &slick_types[id];
        slick_value *fields = slick_xmalloc(sizeof(*fields) * (size_t)(info->field_count ? info->field_count : 1));
        bool *seen = slick_xmalloc(sizeof(*seen) * (size_t)(info->field_count ? info->field_count : 1));
        const char *key;
        json_t *value;
        json_object_foreach(node, key, value) {
            int32_t index = -1;
            for (int32_t i = 0; i < info->field_count; i++) {
                if (strcmp(info->json_names[i], key) == 0) { index = i; break; }
            }
            char *field_path = slick_json_field_path(path, key);
            if (index < 0) { error->path = field_path; error->message = "unknown field"; return 0; }
            const char *field_schema = info->field_schemas[index];
            if (!slick_json_decode_value(value, &field_schema, field_path, &fields[index], error)) return 0;
            seen[index] = true;
        }
        for (int32_t i = 0; i < info->field_count; i++) {
            if (seen[i]) continue;
            if (info->field_schemas[i][0] == '?') {
                fields[i] = slick_rt_none();
                continue;
            }
            error->path = slick_json_field_path(path, info->json_names[i]);
            error->message = "missing required field";
            return 0;
        }
        slick_xfree(seen);
        *out = slick_rt_class(id, info->field_count, fields);
        return 1;
    }
    error->path = slick_xstrdup(path); error->message = "unsupported JSON target type"; return 0;
}

static json_t *slick_json_encode_value(slick_value value, const char **schema, const char *path,
        slick_json_error *error) {
    char kind = *(*schema)++;
    if (kind == '?') {
        if (value.kind != SLICK_OPTIONAL || value.flags == 0) return json_null();
        return slick_json_encode_value(slick_payload(value), schema, path, error);
    }
    if (kind == '[') {
        slick_array *array = slick_as_array(value);
        if (!array) { error->path = slick_xstrdup(path); error->message = "unsupported JSON source type"; return NULL; }
        const char *element_schema = *schema;
        json_t *out = json_array();
        for (int64_t i = 0; i < array->len; i++) {
            const char *element = element_schema;
            char *item_path = slick_json_index_path(path, (size_t)i);
            json_t *item = slick_json_encode_value(array->items[i], &element, item_path, error);
            if (!item) { json_decref(out); return NULL; }
            json_array_append_new(out, item);
        }
        return out;
    }
    if (kind == 'n') return json_null();
    if (kind == 'b') return json_boolean(value.bits != 0);
    if (kind == 's') return json_stringn(slick_cstr(value), (size_t)slick_clen(value));
    if (kind == 'i') return json_integer((json_int_t)value.bits);
    if (kind == 'f') {
        double number = slick_as_float(value);
        if (!isfinite(number)) { error->path = slick_xstrdup(path); error->message = "non-finite float cannot be encoded as JSON"; return NULL; }
        return json_real(number);
    }
    if (kind == 'c') {
        int32_t id = slick_json_schema_class(schema);
        slick_class *object = slick_as_class(value);
        if (id < 0 || !object) { error->path = slick_xstrdup(path); error->message = "unsupported JSON source type"; return NULL; }
        const slick_type_info *info = &slick_types[id];
        json_t *out = json_object();
        for (int32_t i = 0; i < info->field_count; i++) {
            slick_value field = object->fields[i];
            const char *field_schema = info->field_schemas[i];
            if (field_schema[0] == '?' && (field.kind != SLICK_OPTIONAL || field.flags == 0)) continue;
            char *field_path = slick_json_field_path(path, info->json_names[i]);
            json_t *encoded = slick_json_encode_value(field, &field_schema, field_path, error);
            if (!encoded) { json_decref(out); return NULL; }
            json_object_set_new(out, info->json_names[i], encoded);
        }
        return out;
    }
    error->path = slick_xstrdup(path); error->message = "unsupported JSON source type"; return NULL;
}

static char *slick_json_duplicate_key(const char *input, size_t length, size_t position) {
    if (position > length) position = length;
    size_t end = position;
    while (end > 0 && input[end - 1] != '"') end--;
    if (end == 0) return slick_xstrdup("");
    size_t start = end - 1;
    while (start > 0) {
        start--;
        if (input[start] == '"' && (start == 0 || input[start - 1] != '\\')) break;
    }
    size_t n = end > start + 1 ? end - start - 2 : 0;
    char *key = slick_xmalloc(n + 1);
    memcpy(key, input + start + 1, n);
    key[n] = 0;
    return key;
}

slick_outcome slick_nat_json_decode(slick_ctx *ctx, slick_value *args, const char *schema) {
    (void)ctx;
    const char *input = slick_cstr(args[0]);
    size_t length = (size_t)slick_clen(args[0]);
    json_error_t parse;
    json_t *tree = json_loadb(input, length, JSON_DECODE_ANY | JSON_REJECT_DUPLICATES, &parse);
    if (!tree) {
        const char *message = parse.text;
        char *path = slick_xstrdup("$");
        if (length == 0) message = "unexpected end of JSON input";
        else if (strstr(parse.text, "end of file expected")) message = "input contains more than one JSON value";
        else if (strstr(parse.text, "duplicate object key")) {
            char *key = slick_json_duplicate_key(input, length, parse.position);
            path = slick_json_field_path("$", key);
            message = "duplicate object key";
        } else if (schema[0] == 'i' && strstr(parse.text, "integer")) {
            message = "integer out of int64 range";
        } else if (strstr(parse.text, "end of file") || strstr(parse.text, "end of input")) {
            message = "unexpected end of JSON input";
        }
        return slick_ok(slick_rt_result(0, slick_json_failure("Decode", path, message)));
    }
    slick_json_error error = {0};
    slick_value value;
    const char *cursor = schema;
    if (!slick_json_decode_value(tree, &cursor, "$", &value, &error)) {
        json_decref(tree);
        return slick_ok(slick_rt_result(0, slick_json_failure("Decode", error.path, error.message)));
    }
    json_decref(tree);
    return slick_ok(slick_rt_result(1, value));
}

slick_outcome slick_nat_json_encode(slick_ctx *ctx, slick_value *args, const char *schema) {
    (void)ctx;
    slick_json_error error = {0};
    const char *cursor = schema;
    json_t *tree = slick_json_encode_value(args[0], &cursor, "$", &error);
    if (!tree) return slick_ok(slick_rt_result(0, slick_json_failure("Encode", error.path, error.message)));
    char *text = json_dumps(tree, JSON_COMPACT | JSON_SORT_KEYS | JSON_ENCODE_ANY);
    json_decref(tree);
    if (!text) return slick_ok(slick_rt_result(0, slick_json_failure("Encode", "$", "failed to encode JSON")));
    slick_value value = slick_rt_string(text, -1);
    free(text);
    return slick_ok(slick_rt_result(1, value));
}
#endif

void slick_rt_format_p(slick_value *o, slick_value *a) { *o = slick_rt_format(*a); }
void slick_rt_write_error_p(slick_value *a) {
    slick_value formatted = slick_rt_error_format(*a);
    slick_rt_write_bytes(formatted, 2);
    fputc('\n', stderr);
}
void slick_rt_not_p(slick_value *o, slick_value *a) { *o = slick_rt_not(*a); }
void slick_rt_neg_p(slick_value *o, slick_value *a) { *o = slick_rt_neg(*a); }
void slick_rt_add_p(slick_value *o, slick_value *a, slick_value *b) { *o = slick_rt_add(*a, *b); }
void slick_rt_sub_p(slick_value *o, slick_value *a, slick_value *b) { *o = slick_rt_sub(*a, *b); }
void slick_rt_iter_at_p(slick_value *o, slick_value *a, int64_t i, int32_t slot) { *o = slick_rt_iter_at(*a, i, slot); }
void slick_rt_mul_p(slick_value *o, slick_value *a, slick_value *b) { *o = slick_rt_mul(*a, *b); }
void slick_rt_cmp_p(slick_value *o, slick_value *a, slick_value *b, int32_t op) { *o = slick_rt_cmp(*a, *b, op); }
void slick_rt_some_p(slick_value *o, slick_value *a) { *o = slick_rt_some(*a); }
void slick_rt_field_p(slick_value *o, slick_value *a, int32_t i) { *o = slick_rt_field(*a, i); }
void slick_rt_optional_value_p(slick_value *o, slick_value *a) { *o = slick_rt_optional_value(*a); }
void slick_rt_result_payload_p(slick_value *o, slick_value *a) { *o = slick_rt_result_payload(*a); }
void slick_rt_result_p(slick_value *o, slick_value *a, int32_t ok) { *o = slick_rt_result(ok, *a); }
int32_t slick_rt_result_ok_p(slick_value *a) { return slick_rt_result_ok(*a); }
void slick_rt_iter_enum_p(slick_value *o, slick_value *a) { *o = slick_rt_iter_enum(*a); }
void slick_rt_iter_item_p(slick_value *o, slick_value *a, int64_t i) { *o = slick_rt_iter_item(*a, i); }
void slick_rt_iter_of_p(slick_value *o, slick_value *a) { *o = slick_rt_iter_of(*a); }
int64_t slick_rt_iter_len_p(slick_value *a) { return slick_rt_iter_len(*a); }
void slick_rt_union_field_p(slick_value *o, slick_value *a, int32_t i) { *o = slick_rt_union_field(*a, i); }
void slick_rt_array_index_p(slick_value *o, slick_value *a, int64_t i) { *o = slick_rt_array_index(*a, i); }
void slick_rt_array_get_p(slick_value *o, slick_value *a, int64_t i) { *o = slick_rt_array_get(*a, i); }
void slick_rt_map_get_p(slick_value *o, slick_value *a, slick_value *b) { *o = slick_rt_map_get(*a, *b); }
void slick_rt_map_with_p(slick_value *o, slick_value *a, slick_value *b, slick_value *c) { *o = slick_rt_map_with(*a, *b, *c); }
void slick_rt_map_without_p(slick_value *o, slick_value *a, slick_value *b) { *o = slick_rt_map_without(*a, *b); }
int32_t slick_rt_map_contains_p(slick_value *a, slick_value *b) { return slick_rt_map_contains(*a, *b); }
int64_t slick_rt_map_len_p(slick_value *a) { return slick_rt_map_len(*a); }
int64_t slick_rt_array_len_p(slick_value *a) { return slick_rt_array_len(*a); }
void slick_rt_buffer_get_p(slick_value *o, slick_value *a, int64_t i) { *o = slick_rt_buffer_get(*a, i); }
void slick_rt_buffer_freeze_p(slick_value *o, slick_value *a) { *o = slick_rt_buffer_freeze(*a); }
void slick_rt_suppress_p(slick_value *o, slick_value *a, slick_value *b) { *o = slick_rt_suppress(*a, *b); }
int32_t slick_rt_equal_p(slick_value *a, slick_value *b) { return slick_rt_equal(*a, *b); }
int32_t slick_rt_truth_p(slick_value *a) { return slick_rt_truth(*a); }
int32_t slick_rt_class_type_p(slick_value *a) { return slick_rt_class_type(*a); }
void slick_rt_print_p(slick_value *a) { slick_rt_print(*a); }
void slick_rt_write_bytes_p(slick_value *a, int32_t fd) { slick_rt_write_bytes(*a, fd); }
