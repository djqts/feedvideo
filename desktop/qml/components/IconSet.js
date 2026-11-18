.pragma library

// Line icons on a 24x24 grid, 1.5 stroke, square caps and miter joins.
// Drawn as SVG paths so they take the theme colour directly; no icon font and
// no raster assets, which keeps them crisp at every scale factor.

var paths = {
    "play": "M8 5 L19 12 L8 19 Z",
    "pause": "M9 5 L9 19 M15 5 L15 19",
    "stop": "M6 6 L18 6 L18 18 L6 18 Z",
    "search": "M11 4 A7 7 0 1 0 11 18 A7 7 0 1 0 11 4 M16.2 16.2 L21 21",
    "bell": "M6 17 L18 17 L16 14 L16 10 A4 4 0 0 0 8 10 L8 14 Z M10.4 19.2 A1.6 1.6 0 0 0 13.6 19.2",
    "user": "M12 12 A4 4 0 1 0 12 4 A4 4 0 1 0 12 12 M5 20 C5 16 8 14 12 14 C16 14 19 16 19 20",
    "settings": "M12 8.6 A3.4 3.4 0 1 0 12 15.4 A3.4 3.4 0 1 0 12 8.6 M12 3 L12 6 M12 18 L12 21 M3 12 L6 12 M18 12 L21 12",
    "heart": "M12 20 C12 20 4 15 4 9.5 A4 4 0 0 1 12 7 A4 4 0 0 1 20 9.5 C20 15 12 20 12 20 Z",
    "bookmark": "M6 4 L18 4 L18 20 L12 15 L6 20 Z",
    "comment": "M4 5 L20 5 L20 16 L10 16 L6 20 L6 16 L4 16 Z",
    "share": "M8 16 L16 8 M10 8 L16 8 L16 14",
    "more": "M6 11 L6 13 M12 11 L12 13 M18 11 L18 13",
    "close": "M6 6 L18 18 M18 6 L6 18",
    "minimize": "M6 12 L18 12",
    "maximize": "M6 6 L18 6 L18 18 L6 18 Z",
    "restore": "M4 9 L15 9 L15 20 L4 20 Z M8 9 L8 4 L20 4 L20 15 L15 15",
    "chevron-left": "M15 5 L8 12 L15 19",
    "chevron-right": "M9 5 L16 12 L9 19",
    "chevron-down": "M5 9 L12 16 L19 9",
    "refresh": "M20 12 A8 8 0 1 1 12 4 M12 4 L12 8 M12 4 L8 4",
    "upload": "M12 16 L12 4 M7 9 L12 4 L17 9 M5 20 L19 20",
    "clock": "M12 4 A8 8 0 1 0 12 20 A8 8 0 1 0 12 4 M12 8 L12 12 L15 14",
    "grid": "M4 4 L10 4 L10 10 L4 10 Z M14 4 L20 4 L20 10 L14 10 Z M4 14 L10 14 L10 20 L4 20 Z M14 14 L20 14 L20 20 L14 20 Z",
    "list": "M4 6 L20 6 M4 12 L20 12 M4 18 L20 18",
    "volume": "M4 10 L8 10 L13 6 L13 18 L8 14 L4 14 Z M16 9 L18 12 L16 15",
    "fullscreen": "M4 9 L4 4 L9 4 M15 4 L20 4 L20 9 M20 15 L20 20 L15 20 M9 20 L4 20 L4 15",
    "check": "M5 12 L10 17 L19 7",
    "plus": "M12 5 L12 19 M5 12 L19 12",
    "filter": "M4 6 L20 6 L14 13 L14 19 L10 17 L10 13 Z",
    "trash": "M5 7 L19 7 M9 7 L9 4 L15 4 L15 7 M7 7 L8 20 L16 20 L17 7",
    "flag": "M6 4 L6 20 M6 5 L18 5 L14 10 L18 15 L6 15",
    "eye": "M2 12 C6 6 18 6 22 12 C18 18 6 18 2 12 M12 9 A3 3 0 1 0 12 15 A3 3 0 1 0 12 9",
    "arrow-left": "M20 12 L4 12 M10 6 L4 12 L10 18",
    "dots-grid": "M6 6 L6 6.1 M12 6 L12 6.1 M18 6 L18 6.1 M6 12 L6 12.1 M12 12 L12 12.1 M18 12 L18 12.1 M6 18 L6 18.1 M12 18 L12 18.1 M18 18 L18 18.1"
}

function path(name) {
    return paths[name] !== undefined ? paths[name] : ""
}

function has(name) {
    return paths[name] !== undefined
}
