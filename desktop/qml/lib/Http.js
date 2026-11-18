.pragma library

// The gateway answers with { code, data, message }. Some endpoints put a list in
// data, some wrap it as { list, total, page }. These helpers flatten that so a
// screen can always ask for "the items" and "the total".

// A QVariantList arriving from C++ is array-like but Array.isArray() is false in
// QML, so every list check has to go through these two helpers.
function isList(value) {
    return !!value && typeof value === "object" && typeof value.length === "number"
}

function toArray(value) {
    var out = []
    if (!isList(value))
        return out
    for (var i = 0; i < value.length; i++)
        out.push(value[i])
    return out
}

function items(res) {
    if (!res)
        return []
    if (isList(res))
        return toArray(res)
    if (res.data === undefined)
        return []
    var d = res.data
    if (isList(d))
        return toArray(d)
    if (d === null || typeof d !== "object")
        return []
    if (isList(d.list))
        return toArray(d.list)
    if (isList(d.items))
        return toArray(d.items)
    if (isList(d.records))
        return toArray(d.records)
    if (d.data !== undefined)
        return items({ data: d.data })
    return []
}

function total(res) {
    if (!res || !res.data || typeof res.data !== "object")
        return -1
    if (typeof res.data.total === "number")
        return res.data.total
    if (typeof res.data.count === "number")
        return res.data.count
    return -1
}// A feed entry wraps the video and keeps the counters on the entry itself, so
// merge them in before anything reads play_count like_count.
function video(item) {
    if (!item)
        return null
    var base = (item.video && typeof item.video === "object") ? item.video : item
    if (item.stats && typeof item.stats === "object") {
        var merged = {}
        for (var key in base)
            merged[key] = base[key]
        for (var statKey in item.stats)
            merged[statKey] = item.stats[statKey]
        if (item.is_liked !== undefined)
            merged.is_liked = item.is_liked
        if (item.is_collected !== undefined)
            merged.is_collected = item.is_collected
        return merged
    }
    return base
}

function num(value) {
    var n = Number(value)
    return isFinite(n) ? n : 0
}

function count(value) {
    var n = num(value)
    if (n >= 10000)
        return (n / 10000).toFixed(1).replace(/\.0$/, "") + "万"
    return String(n)
}

function duration(seconds) {
    var s = Math.round(num(seconds))
    if (s <= 0)
        return "--:--"
    var m = Math.floor(s / 60)
    var r = s % 60
    return (m < 10 ? "0" + m : m) + ":" + (r < 10 ? "0" + r : r)
}

function date(value) {
    if (!value)
        return ""
    var d = new Date(value)
    if (isNaN(d.getTime()))
        return String(value)
    var y = d.getFullYear()
    var m = d.getMonth() + 1
    var day = d.getDate()
    return y + "." + (m < 10 ? "0" + m : m) + "." + (day < 10 ? "0" + day : day)
}

function pad(n, width) {
    var s = String(n)
    while (s.length < width)
        s = "0" + s
    return s
}

function tag(index) {
    return "No." + pad(index, 3)
}

function firstOf() {
    for (var i = 0; i < arguments.length; i++) {
        var v = arguments[i]
        if (v !== undefined && v !== null && v !== "")
            return v
    }
    return ""
}

// Absolute media URL: the gateway returns /storage/... paths, and the desktop
// client talks to the same origin.
// A library JS file cannot read QML singletons, so callers pass the origin in.
function mediaUrl(path, mediaBase, apiBase) {
    if (!path)
        return ""
    if (path.indexOf("http") === 0)
        return path
    // Object storage paths look like /storage/<bucket>/... and the gateway does
    // not proxy them; the bucket root is configurable.
    if (path.indexOf("/storage/") === 0)
        return (mediaBase || "") + path.substring(8)
    var base = apiBase || ""
    var idx = base.indexOf("/api")
    if (idx > 0)
        base = base.substring(0, idx)
    return base + path
}

// The gateway nests counters under stats on detail responses but flattens them
// on list responses, and it never sends an author name — only an id.
function stats(video) {
    return (video && video.stats) || {}
}

function metric(video, key) {
    if (!video)
        return 0
    if (video[key] !== undefined)
        return num(video[key])
    return num(stats(video)[key])
}

function plays(video) {
    return metric(video, "play_count")
}

function likes(video) {
    return metric(video, "like_count")
}

function comments(video) {
    return metric(video, "comment_count")
}

function collects(video) {
    return metric(video, "collect_count")
}

function author(video) {
    if (!video)
        return ""
    var name = firstOf(video.author_name, video.nickname, video.user_name, "")
    if (name !== "")
        return name
    var id = firstOf(video.author_id, video.user_id, video.uid, "")
    return id !== "" ? "作者 " + id : "未知作者"
}

// A day count for the last N items, used by the console's bar line.
function lastValues(list, key, count) {
    var out = []
    var start = Math.max(0, list.length - count)
    for (var i = start; i < list.length; i++)
        out.push(metric(list[i], key))
    return out
}
