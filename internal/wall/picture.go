// Package wall is the terminal "wall": a newest-first feed of what agents
// produced for one project (commits, images, other files), shown in a pane
// beside the work.
package wall

import "github.com/base698/amythest/internal/termimg"

// Image drawing lives in termimg, shared with the Messages client.
var (
	decode        = termimg.Decode
	boxFor        = termimg.BoxFor
	shrink        = termimg.Shrink
	kittyTransmit = termimg.KittyTransmit
	kittyDelete   = termimg.KittyDelete
	kittyRows     = termimg.KittyRows
	blockRows     = termimg.BlockRows
	encodePNG     = termimg.EncodePNG
	diacritics    = termimg.Diacritics
)

const (
	placeholder = termimg.Placeholder
	maxCells    = termimg.MaxCells
)
