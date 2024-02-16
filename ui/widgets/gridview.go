package widgets

import (
	"context"
	"math"
	"sync"

	"github.com/dweymouth/supersonic/backend/mediaprovider"
	"github.com/dweymouth/supersonic/sharedutil"
	myTheme "github.com/dweymouth/supersonic/ui/theme"
	"github.com/dweymouth/supersonic/ui/util"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const batchFetchSize = 6

type BatchingIterator struct {
	iter mediaprovider.AlbumIterator
}

func NewBatchingIterator(iter mediaprovider.AlbumIterator) BatchingIterator {
	return BatchingIterator{iter}
}

func (b *BatchingIterator) NextN(n int) []*mediaprovider.Album {
	results := make([]*mediaprovider.Album, 0, n)
	i := 0
	for i < n {
		album := b.iter.Next()
		if album == nil {
			break
		}
		results = append(results, album)
		i++
	}
	return results
}

type GridViewIterator interface {
	NextN(int) []GridViewItemModel
}

type gridViewAlbumIterator struct {
	iter BatchingIterator
}

func (g gridViewAlbumIterator) NextN(n int) []GridViewItemModel {
	albums := g.iter.NextN(n)
	return sharedutil.MapSlice(albums, func(al *mediaprovider.Album) GridViewItemModel {
		return GridViewItemModel{
			Name:         al.Name,
			ID:           al.ID,
			CoverArtID:   al.CoverArtID,
			Secondary:    al.ArtistNames,
			SecondaryIDs: al.ArtistIDs,
		}
	})
}

func NewGridViewAlbumIterator(iter mediaprovider.AlbumIterator) GridViewIterator {
	return gridViewAlbumIterator{iter: NewBatchingIterator(iter)}
}

type GridView struct {
	widget.BaseWidget

	stateMutex  sync.RWMutex
	fetchCancel context.CancelFunc
	GridViewState

	grid               *disabledGridWrap
	menu               *widget.PopUpMenu
	menuGridViewItemId string
	itemForIndex       map[int]*GridViewItem
	itemWidth          float32
	numColsCached      int
}

type GridViewState struct {
	items        []GridViewItemModel
	iter         GridViewIterator
	imageFetcher util.ImageFetcher
	Placeholder  fyne.Resource
	highestShown int
	done         bool

	OnPlay              func(id string, shuffle bool)
	OnAddToQueue        func(id string)
	OnAddToPlaylist     func(id string)
	OnDownload          func(id string)
	OnShowItemPage      func(id string)
	OnShowSecondaryPage func(id string)

	scrollPos float32
}

var _ fyne.Widget = (*GridView)(nil)

func newGridView() *GridView {
	return &GridView{
		itemWidth:    NewGridViewItem(nil).MinSize().Width,
		itemForIndex: make(map[int]*GridViewItem),
	}
}

func NewFixedGridView(items []GridViewItemModel, fetch util.ImageFetcher, placeholder fyne.Resource) *GridView {
	g := newGridView()
	g.GridViewState = GridViewState{
		items:        items,
		done:         true,
		imageFetcher: fetch,
		Placeholder:  placeholder,
	}
	g.ExtendBaseWidget(g)
	g.createGridWrap()
	return g
}

func NewGridView(iter GridViewIterator, fetch util.ImageFetcher, placeholder fyne.Resource) *GridView {
	g := newGridView()
	g.GridViewState = GridViewState{
		iter:         iter,
		imageFetcher: fetch,
		Placeholder:  placeholder,
	}
	g.ExtendBaseWidget(g)
	g.createGridWrap()

	// fetch initial items
	g.checkFetchMoreItems(36)
	return g
}

func (g *GridView) SaveToState() *GridViewState {
	g.stateMutex.RLock()
	s := g.GridViewState
	g.stateMutex.RUnlock()
	s.scrollPos = g.grid.GetScrollOffset()
	return &s
}

func NewGridViewFromState(state *GridViewState) *GridView {
	g := newGridView()
	g.GridViewState = *state
	g.ExtendBaseWidget(g)
	g.createGridWrap()
	g.Refresh() // needed to initialize the widget
	g.grid.ScrollToOffset(state.scrollPos)
	return g
}

func (g *GridView) Clear() {
	g.stateMutex.Lock()
	defer g.stateMutex.Unlock()
	g.cancelFetch()
	g.items = nil
	g.done = true
}

func (g *GridView) Reset(iter GridViewIterator) {
	g.stateMutex.Lock()
	g.cancelFetch()
	g.items = nil
	g.done = false
	g.highestShown = 0
	g.iter = iter
	g.stateMutex.Unlock()
	g.checkFetchMoreItems(36)
	g.Refresh()
}

func (g *GridView) ResetFromState(state *GridViewState) {
	g.stateMutex.Lock()
	g.cancelFetch()
	g.GridViewState = *state
	g.stateMutex.Unlock()
	g.grid.Refresh()
	g.grid.ScrollToOffset(state.scrollPos)
	g.grid.Refresh()
}

func (g *GridView) ResetFixed(items []GridViewItemModel) {
	g.stateMutex.Lock()
	g.cancelFetch()
	g.items = items
	g.done = true
	g.highestShown = 0
	g.iter = nil
	g.stateMutex.Unlock()
	g.Refresh()
}

func (g *GridView) GetScrollOffset() float32 {
	return g.grid.GetScrollOffset()
}

func (g *GridView) ScrollToOffset(offs float32) {
	g.grid.ScrollToOffset(offs)
}

func (g *GridView) Resize(size fyne.Size) {
	g.numColsCached = -1
	g.BaseWidget.Resize(size)
}

func (g *GridView) numCols() int {
	if g.numColsCached == -1 {
		// logic here taken from gridwrap.go in Fyne codebase
		colCount := 1
		width := g.Size().Width
		if width > g.itemWidth {
			pad := theme.Padding()
			colCount = int(math.Floor(float64(width+pad) / float64(g.itemWidth+pad)))
		}
		g.numColsCached = colCount
	}
	return g.numColsCached
}

func (g *GridView) createGridWrap() {
	g.grid = NewDisabledGridWrap(
		g.lenItems,
		g.createNewItemCard,
		// update func
		func(itemID widget.GridWrapItemID, obj fyne.CanvasObject) {
			ac := obj.(*GridViewItem)
			g.doUpdateItemCard(int(itemID), ac)
		},
	)
}

func (g *GridView) createNewItemCard() fyne.CanvasObject {
	card := NewGridViewItem(g.Placeholder)
	card.ItemIndex = -1
	card.ImgLoader = util.NewThumbnailLoader(g.imageFetcher, card.Cover.SetImage)
	card.ImgLoader.OnBeforeLoad = func() { card.Cover.SetImage(nil) }
	card.OnPlay = func() { g.onPlay(card.ItemID(), false) }
	card.OnShowSecondaryPage = func(id string) {
		if g.OnShowSecondaryPage != nil {
			g.OnShowSecondaryPage(id)
		}
	}
	card.OnShowItemPage = func() {
		if g.OnShowItemPage != nil {
			g.OnShowItemPage(card.ItemID())
		}
	}
	card.OnShowContextMenu = func(p fyne.Position) {
		g.showContextMenu(card, p)
	}
	card.OnFocusNeighbor = func(neighbor int) {
		focusIndex := -1
		switch neighbor {
		case 0: // left
			focusIndex = card.ItemIndex - 1
		case 1: // right
			focusIndex = card.ItemIndex + 1
		case 2: // up
			focusIndex = card.ItemIndex - g.numCols()
		case 3: // down
			focusIndex = card.ItemIndex + g.numCols()
		}
		if focusIndex >= 0 && focusIndex < g.lenItems() {
			g.grid.ScrollTo(focusIndex)
			g.stateMutex.RLock()
			if item, ok := g.itemForIndex[focusIndex]; ok {
				fyne.CurrentApp().Driver().CanvasForObject(g).Focus(item)
			}
			g.stateMutex.RUnlock()
		}
	}
	return card
}

func (g *GridView) doUpdateItemCard(itemIdx int, card *GridViewItem) {
	if itemIdx > g.highestShown {
		g.highestShown = itemIdx
	}
	var item GridViewItemModel
	g.stateMutex.Lock()
	// itemIdx can rarely be out of range if the data is being updated
	// as the view is requested to refresh
	if itemIdx < len(g.items) {
		item = g.items[itemIdx]
	}
	card.Cover.Im.CenterIcon = g.Placeholder
	if !card.NeedsUpdate(item) && card.ItemIndex == itemIdx {
		// nothing to do
		g.stateMutex.Unlock()
		return
	}
	// update itemForIndex map
	if c, ok := g.itemForIndex[card.ItemIndex]; ok && c == card {
		delete(g.itemForIndex, card.ItemIndex)
	}
	card.ItemIndex = itemIdx
	g.itemForIndex[itemIdx] = card
	g.stateMutex.Unlock()
	card.Update(item)
	card.ImgLoader.Load(item.CoverArtID)

	// if user has scrolled near the bottom, fetch more
	if itemIdx > g.lenItems()-10 {
		g.checkFetchMoreItems(20)
	}
}

func (g *GridView) lenItems() int {
	g.stateMutex.RLock()
	defer g.stateMutex.RUnlock()
	return len(g.items)
}

// fetches at least count more items if fetch not in progress and not done
// acquires stateMutex for atomicity
func (g *GridView) checkFetchMoreItems(count int) {
	g.stateMutex.Lock()
	defer g.stateMutex.Unlock()
	if g.done || g.fetchCancel != nil {
		return // done, or fetch already in progress
	}
	if g.iter == nil {
		g.done = true
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	g.fetchCancel = cancel
	go func() {
		// keep repeating the fetch task as long as the user
		// has scrolled near the bottom
		for !g.done && g.highestShown >= g.lenItems()-10 {
			n := 0
			for !g.done && n < count {
				items := g.iter.NextN(batchFetchSize)
				select {
				case <-ctx.Done():
					return
				default:
					g.stateMutex.Lock()
					g.items = append(g.items, items...)
					g.stateMutex.Unlock()
					if len(items) < batchFetchSize {
						g.done = true
					}
					n += len(items)
					if len(items) > 0 {
						g.grid.Refresh()
					}
				}
			}
		}
		// call cancelfunc to release Context resources
		g.stateMutex.Lock()
		g.cancelFetch()
		g.stateMutex.Unlock()
	}()
}

// must be called with stateMutex locked for writing
func (g *GridView) cancelFetch() {
	if g.fetchCancel != nil {
		g.fetchCancel()
		g.fetchCancel = nil
	}
}

func (g *GridView) showContextMenu(card *GridViewItem, pos fyne.Position) {
	g.menuGridViewItemId = card.ItemID()
	if g.menu == nil {
		play := fyne.NewMenuItem("Play", func() { g.onPlay(g.menuGridViewItemId, false) })
		play.Icon = theme.MediaPlayIcon()
		shuffle := fyne.NewMenuItem("Shuffle", func() { g.onPlay(g.menuGridViewItemId, true) })
		shuffle.Icon = myTheme.ShuffleIcon
		queue := fyne.NewMenuItem("Add to queue", func() {
			if g.OnAddToQueue != nil {
				g.OnAddToQueue(g.menuGridViewItemId)
			}
		})
		queue.Icon = theme.ContentAddIcon()
		playlist := fyne.NewMenuItem("Add to playlist...", func() {
			if g.OnAddToPlaylist != nil {
				g.OnAddToPlaylist(g.menuGridViewItemId)
			}
		})
		playlist.Icon = myTheme.PlaylistIcon
		download := fyne.NewMenuItem("Download...", func() {
			if g.OnDownload != nil {
				g.OnDownload(g.menuGridViewItemId)
			}
		})
		download.Icon = theme.DownloadIcon()

		g.menu = widget.NewPopUpMenu(fyne.NewMenu("", play, shuffle, queue, playlist, download),
			fyne.CurrentApp().Driver().CanvasForObject(g))
	}
	g.menu.ShowAtPosition(pos)
}

func (g *GridView) onPlay(itemID string, shuffle bool) {
	if g.OnPlay != nil {
		g.OnPlay(itemID, shuffle)
	}
}

func (g *GridView) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(g.grid)
}

// a disabled widget is not considered focusable by the focus manager
type disabledGridWrap struct {
	widget.GridWrap
}

func NewDisabledGridWrap(len func() int, create func() fyne.CanvasObject, update func(widget.GridWrapItemID, fyne.CanvasObject)) *disabledGridWrap {
	g := &disabledGridWrap{
		GridWrap: widget.GridWrap{
			Length:     len,
			CreateItem: create,
			UpdateItem: update,
		},
	}
	g.ExtendBaseWidget(g)
	return g
}

var _ fyne.Disableable = (*disabledGridWrap)(nil)

func (g *disabledGridWrap) Disabled() bool { return true }

func (g *disabledGridWrap) Disable() {}

func (g *disabledGridWrap) Enable() {}
