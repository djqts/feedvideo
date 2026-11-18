import QtQuick

// Which screen is showing. Kept in one object so the side nav, the title bar and
// the theme all read the same value.
QtObject {
    id: router

    property string current: "feed"
    property string previous: "feed"
    property var params: ({})

    signal navigated(string name)
    // Something the user sees may have just become invalid elsewhere in the app.
    signal videoDeleted(string id)
    // The author's library changed (publish, delete, retry): numbers are stale.
    signal libraryChanged()

    function go(name, args) {
        if (name === current)
            return
        previous = current
        current = name
        params = args || {}
        navigated(name)
    }
}
