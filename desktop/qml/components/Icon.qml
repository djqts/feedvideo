import QtQuick
import QtQuick.Shapes
import FeedVideo.Core 1.0
import "IconSet.js" as IconSet

// A stroked line icon. Sizes are logical pixels; the stroke stays 1.5 no matter
// how large the icon is drawn, which is what makes the set read as one family.
Item {
    id: root

    property string name: ""
    property color color: Theme.ink
    property real size: 18
    property real weight: 1.5

    implicitWidth: size
    implicitHeight: size

    Shape {
        width: 24
        height: 24
        preferredRendererType: Shape.CurveRenderer
        transform: Scale {
            origin.x: 0
            origin.y: 0
            xScale: root.size / 24
            yScale: root.size / 24
        }

        ShapePath {
            strokeColor: root.color
            strokeWidth: root.weight * 24 / root.size
            fillColor: "transparent"
            capStyle: ShapePath.SquareCap
            joinStyle: ShapePath.MiterJoin
            PathSvg { path: IconSet.path(root.name) }
        }
    }
}
