import QtQuick
import FeedVideo.Core 1.0
import "../components"
import "../lib/Http.js" as Http

// Everything you marked to keep. Same archive rows as the feed, with the bay
// opening in place.
Item {
    id: root

    property var router: null
    property var rows: []
    property bool loading: false
    property string errorText: ""
    property int selectedIndex: -1
    property int page: 1
    property bool hasMore: false
    property bool loadingMore: false

    function load(reset) {
        if (reset) {
            page = 1
            rows = []
            selectedIndex = -1
            loading = true
        } else {
            loadingMore = true
        }
        errorText = ""
        Api.requestQuery("collect.list", "/v1/collect/list", { page: page, size: 20 })
    }

    function loadMore() {
        if (loadingMore || !hasMore)
            return
        page = page + 1
        load(false)
    }

    Component.onCompleted: load(true)

    Connections {
        target: Api

        function onResult(tag, data) {
            if (tag !== "collect.list")
                return
            root.loading = false
            root.loadingMore = false
            var payload = data && data.data ? data.data : data
            var batch = Http.items(data)
            root.hasMore = !!(payload && payload.has_more)
            root.rows = root.page > 1 ? root.rows.concat(batch) : batch
        }

        function onFailed(tag, status, code, message) {
            if (tag !== "collect.list")
                return
            root.loading = false
            root.loadingMore = false
            root.errorText = status === 401 ? qsTr("登录之后才能看到收藏。") : message
        }
    }

    Column {
        anchors.fill: parent
        anchors.margins: Theme.pagePad
        spacing: 0

        Row {
            width: parent.width

            Column {
                spacing: 6
                width: parent.width - refreshButton.width - 20

                PairTitle { en: "SAVED"; cn: "收藏" }
                FieldRow { fields: [root.rows.length + qsTr(" 条")] }
            }

            ActionButton {
                id: refreshButton
                anchors.verticalCenter: parent.verticalCenter
                label: qsTr("刷新")
                icon: "refresh"
                variant: "ghost"
                cornerColor: Theme.paper
                onClicked: root.load(true)
            }
        }

        Item { height: 14; width: 1 }
        TriStrip { width: 160 }
        Item { height: 12; width: 1 }

        Notice {
            width: parent.width
            visible: root.errorText !== ""
            text: root.errorText
            actionLabel: Api.authenticated ? qsTr("重试") : qsTr("去登录")
            onActionClicked: {
                if (Api.authenticated)
                    root.load()
                else if (root.router)
                    root.router.go("login")
            }
        }

        Item {
            width: parent.width
            height: parent.height - y - 6

            ListView {
                id: list
                anchors.fill: parent
                clip: true
                model: root.rows
                boundsBehavior: Flickable.StopAtBounds

                delegate: FeedRow {
                    required property var modelData
                    required property int index

                    width: list.width
                    video: Http.video(modelData)
                    number: Http.tag(index + 1)
                    expanded: root.selectedIndex === index
                    onActivated: root.selectedIndex = (root.selectedIndex === index ? -1 : index)
                }

                footer: Item {
                    width: list.width
                    visible: root.hasMore && root.rows.length > 0
                    height: visible ? 56 : 0

                    ActionButton {
                        anchors.centerIn: parent
                        label: root.loadingMore ? qsTr("读取中…") : qsTr("加载更多")
                        variant: "ghost"
                        cornerColor: Theme.paper
                        enabled: !root.loadingMore
                        onClicked: root.loadMore()
                    }
                }
            }

            Text {
                anchors.centerIn: parent
                visible: !root.loading && root.rows.length === 0 && root.errorText === ""
                text: qsTr("还没有收藏。看到想留着的，点一下书签。")
                color: Theme.ink2
                font.family: Theme.fontText
                font.pixelSize: 14
                renderType: Text.NativeRendering
            }
        }
    }
}
