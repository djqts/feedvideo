import QtQuick
import FeedVideo.Core 1.0
import "../components"
import "../lib/Http.js" as Http

// What happened while you were not looking. Unread items carry a small accent
// block, and "全部标记已读" is the one primary action on the screen.
Item {
    id: root

    property var router: null
    property var rows: []
    property int unread: 0
    property bool loading: false
    property string errorText: ""
    property int page: 1
    property int total: -1
    property bool loadingMore: false

    function load(reset) {
        if (reset) {
            page = 1
            rows = []
            loading = true
        } else {
            loadingMore = true
        }
        Api.requestQuery("notify.list", "/v1/notifications", { page: page, page_size: 20 })
        if (reset)
            Api.request("notify.unread", "GET", "/v1/notifications/unread-count")
    }

    function loadMore() {
        if (loadingMore || !(total >= 0 ? rows.length < total : rows.length >= 20))
            return
        page = page + 1
        load(false)
    }

    Component.onCompleted: load(true)

    Connections {
        target: Api

        function onResult(tag, data) {
            if (tag === "notify.list") {
                loading = false
                loadingMore = false
                var payload = data && data.data ? data.data : data
                var batch = Http.items(data)
                root.rows = root.page > 1 ? root.rows.concat(batch) : batch
                if (payload && payload.total !== undefined)
                    root.total = Http.num(payload.total)
            } else if (tag === "notify.unread") {
                var payload = data && data.data ? data.data : data
                root.unread = Http.num(payload && (payload.count !== undefined ? payload.count : payload.unread))
            } else if (tag === "notify.read") {
                root.unread = 0
                for (var i = 0; i < root.rows.length; i++) {
                    root.rows[i].read = true
                }
                root.rows = root.rows.slice(0)
            }
        }

        function onFailed(tag, status, code, message) {
            if (tag === "notify.list") {
                root.loading = false
                root.loadingMore = false
                root.errorText = status === 401 ? qsTr("登录之后才能看到通知。") : message
            }
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
                width: parent.width - markButton.width - 20

                PairTitle { en: "NOTIFY"; cn: "通知" }
                FieldRow {
                    fields: [
                        root.unread > 0 ? root.unread + qsTr(" 条未读") : qsTr("全部已读"),
                        root.rows.length + qsTr(" 条")
                    ]
                }
            }

            ActionButton {
                id: markButton
                anchors.verticalCenter: parent.verticalCenter
                label: qsTr("全部标记已读")
                icon: "check"
                variant: "ghost"
                cornerColor: Theme.paper
                onClicked: Api.request("notify.read", "POST", "/v1/notifications/read", {})
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
                    root.load(true)
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

                delegate: Item {
                    required property var modelData
                    required property int index

                    width: list.width
                    height: 72

                    readonly property bool unread: !modelData.read && !modelData.is_read

                    Rectangle {
                        width: 4
                        height: parent.height - 16
                        y: 8
                        color: parent.unread ? Theme.signal : "transparent"
                    }

                    Column {
                        anchors.left: parent.left
                        anchors.leftMargin: 18
                        anchors.verticalCenter: parent.verticalCenter
                        width: parent.width - 160
                        spacing: 4

                        Text {
                            text: Http.firstOf(modelData.title, modelData.type, qsTr("通知"))
                            color: Theme.ink
                            font.family: Theme.fontText
                            font.pixelSize: 14
                            font.weight: parent.parent.unread ? Font.Bold : Font.DemiBold
                            elide: Text.ElideRight
                            width: parent.width
                            renderType: Text.NativeRendering
                        }

                        Text {
                            text: Http.firstOf(modelData.content, modelData.message, modelData.body, "")
                            color: Theme.ink2
                            font.family: Theme.fontText
                            font.pixelSize: 12
                            elide: Text.ElideRight
                            width: parent.width
                            renderType: Text.NativeRendering
                        }
                    }

                    FieldRow {
                        anchors.right: parent.right
                        anchors.rightMargin: 16
                        anchors.verticalCenter: parent.verticalCenter
                        fields: [Http.firstOf(Http.date(modelData.created_at), "")]
                        size: 11
                    }

                    Rectangle {
                        anchors.bottom: parent.bottom
                        width: parent.width
                        height: 1
                        color: Theme.rule
                    }
                }

                footer: Item {
                    width: list.width
                    visible: root.rows.length > 0 && root.total >= 0 && root.rows.length < root.total
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
                text: qsTr("还没有通知。有人评论或关注你时，会出现在这里。")
                color: Theme.ink2
                font.family: Theme.fontText
                font.pixelSize: 14
                renderType: Text.NativeRendering
            }
        }
    }
}
