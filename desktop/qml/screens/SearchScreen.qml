import QtQuick
import FeedVideo.Core 1.0
import "../components"
import "../lib/Http.js" as Http

// Finding something. The field is paper with a hairline that turns into a 2px
// trace line while it has focus; results are the same archive rows as the feed.
Item {
    id: root

    property var router: null
    property string keyword: ""
    property var results: []
    property var hotWords: []
    property bool loading: false
    property bool searched: false
    property string errorText: ""
    property int selectedIndex: -1

    function search() {
        if (keyword.trim() === "")
            return
        loading = true
        searched = true
        errorText = ""
        selectedIndex = -1
        Api.requestQuery("search.page", "/v1/search", { q: keyword, page: 1, size: 30 })
    }

    function words(list) {
        var out = []
        for (var i = 0; i < list.length; i++) {
            var item = list[i]
            var text = ""
            if (typeof item === "string") {
                text = item
            } else if (item && typeof item === "object") {
                text = Http.firstOf(item.word, item.keyword, item.name, item.title, "")
            }
            if (text !== "" && text.indexOf("\ufffd") < 0)
                out.push(String(text))
        }
        return out
    }

    Component.onCompleted: {
        Api.requestQuery("search.hot", "/v1/search/hot", {})
        Api.requestQuery("search.topics", "/v1/topics/hot", {})
        field.forceActiveFocus()
    }

    Connections {
        target: root.router
        function onNavigated(name) {
            if (name === "search")
                field.forceActiveFocus()
        }
    }

    Connections {
        target: Api

        function onResult(tag, data) {
            if (tag === "search.page") {
                root.loading = false
                root.results = Http.items(data)
            } else if (tag === "search.hot") {
                var hot = root.words(Http.items(data))
                if (hot.length > 0)
                    root.hotWords = hot
            } else if (tag === "search.topics") {
                if (root.hotWords.length === 0)
                    root.hotWords = root.words(Http.items(data))
            }
        }

        function onFailed(tag, status, code, message) {
            if (tag === "search.page") {
                root.loading = false
                root.errorText = message
            }
        }
    }

    Column {
        anchors.fill: parent
        anchors.margins: Theme.pagePad
        spacing: 0

        PairTitle { en: "SEARCH"; cn: "搜索" }

        Shortcut {
            sequence: "Ctrl+K"
            onActivated: field.forceActiveFocus()
        }

        Item { height: 14; width: 1 }

        // Field
        Rectangle {
            width: Math.min(parent.width, 620)
            height: 46
            color: Theme.panel
            border.color: field.activeFocus ? Theme.trace : Theme.rule
            border.width: field.activeFocus ? 2 : 1

            Row {
                anchors.fill: parent
                anchors.leftMargin: 14
                anchors.rightMargin: 14
                spacing: 10

                Text {
                    anchors.verticalCenter: parent.verticalCenter
                    text: "//"
                    color: Theme.ink2
                    font.family: Theme.fontWide
                    font.pixelSize: 13
                    font.weight: Font.DemiBold
                    renderType: Text.NativeRendering
                }

                TextInput {
                    id: field
                    anchors.verticalCenter: parent.verticalCenter
                    width: parent.width - 80
                    text: root.keyword
                    color: Theme.ink
                    font.family: Theme.fontText
                    font.pixelSize: 15
                    selectByMouse: true
                    selectionColor: Theme.signal
                    selectedTextColor: Theme.onSignal
                    onTextChanged: root.keyword = text
                    onAccepted: root.search()
                    Keys.onEscapePressed: focus = false

                    Text {
                        anchors.verticalCenter: parent.verticalCenter
                        visible: field.text === ""
                        text: qsTr("标题、作者或话题")
                        color: Theme.ink2
                        font.family: Theme.fontText
                        font.pixelSize: 15
                        renderType: Text.NativeRendering
                    }
                }
            }
        }

        Item { height: 12; width: 1 }

        // Hot words / categories — kept as plain strings so the chips never
        // have to guess at object shapes.
        Row {
            spacing: 8
            visible: root.hotWords.length > 0 && root.results.length === 0

            Repeater {
                model: root.hotWords.slice(0, 6)

                delegate: FilterChip {
                    required property string modelData
                    label: modelData
                    onToggled: {
                        root.keyword = modelData
                        root.search()
                    }
                }
            }
        }

        Item { height: 14; width: 1 }

        Notice {
            width: parent.width
            visible: root.errorText !== ""
            text: root.errorText
            actionLabel: qsTr("重试")
            onActionClicked: root.search()
        }

        // Results
        Item {
            width: parent.width
            height: parent.height - y - 8

            ListView {
                id: list
                anchors.fill: parent
                clip: true
                model: root.results
                boundsBehavior: Flickable.StopAtBounds

                delegate: FeedRow {
                    required property var modelData
                    required property int index

                    width: list.width
                    video: Http.video(modelData)
                    number: Http.tag(index + 1)
                    expanded: root.selectedIndex === index
                    onActivated: {
                        if (root.selectedIndex === index) {
                            root.selectedIndex = -1
                        } else {
                            root.selectedIndex = index
                        }
                    }
                }
            }

            Text {
                anchors.centerIn: parent
                visible: !root.loading && root.results.length === 0 && root.searched && root.errorText === ""
                text: qsTr("没有找到匹配的视频。换个词试试，或者从推荐里翻。")
                color: Theme.ink2
                font.family: Theme.fontText
                font.pixelSize: 14
                renderType: Text.NativeRendering
            }

            Text {
                anchors.centerIn: parent
                visible: !root.searched
                text: qsTr("输入一个词，按回车开始找。")
                color: Theme.ink2
                font.family: Theme.fontText
                font.pixelSize: 14
                renderType: Text.NativeRendering
            }
        }
    }
}
