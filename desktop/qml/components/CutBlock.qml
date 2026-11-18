import QtQuick
import QtQuick.Shapes
import FeedVideo.Core 1.0

// The shape grammar of the whole product: a flat block that loses one or more
// corners. A cut corner means "pressable"; content blocks stay square.
// Radius is always 0.
Item {
    id: root

    property color fill: Theme.panel
    property color strokeColor: "transparent"
    property real strokeWidth: 0
    property real cut: Theme.cutButton
    // Bitmask of Qt.TopLeftCorner / Qt.TopRightCorner / Qt.BottomLeftCorner / Qt.BottomRightCorner
    property int corners: Qt.BottomRightCorner

    default property alias content: contentHolder.data

    function cutFor(corner) {
        return (corners & corner) ? cut : 0
    }

    // Inset by half the stroke so a 1px border is not clipped at the edges.
    readonly property real inset: strokeWidth > 0 ? strokeWidth / 2 : 0

    function buildPath() {
        var w = width - inset * 2
        var h = height - inset * 2
        var tl = cutFor(Qt.TopLeftCorner)
        var tr = cutFor(Qt.TopRightCorner)
        var bl = cutFor(Qt.BottomLeftCorner)
        var br = cutFor(Qt.BottomRightCorner)
        var pts = []

        if (tl > 0) {
            pts.push([inset, inset + tl])
            pts.push([inset + tl, inset])
        } else {
            pts.push([inset, inset])
        }
        if (tr > 0) {
            pts.push([inset + w - tr, inset])
            pts.push([inset + w, inset + tr])
        } else {
            pts.push([inset + w, inset])
        }
        if (br > 0) {
            pts.push([inset + w, inset + h - br])
            pts.push([inset + w - br, inset + h])
        } else {
            pts.push([inset + w, inset + h])
        }
        if (bl > 0) {
            pts.push([inset + bl, inset + h])
            pts.push([inset, inset + h - bl])
        } else {
            pts.push([inset, inset + h])
        }

        var d = "M" + pts[0][0] + " " + pts[0][1]
        for (var i = 1; i < pts.length; i++)
            d += " L" + pts[i][0] + " " + pts[i][1]
        return d + " Z"
    }

    Shape {
        anchors.fill: parent
        preferredRendererType: Shape.CurveRenderer

        ShapePath {
            fillColor: root.fill
            strokeColor: root.strokeColor
            strokeWidth: root.strokeWidth
            joinStyle: ShapePath.MiterJoin
            PathSvg { path: root.buildPath() }
        }
    }

    Item {
        id: contentHolder
        anchors.fill: parent
    }
}
