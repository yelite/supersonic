package widgets

import (
	"image"
	"strconv"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/dweymouth/supersonic/backend"
	"github.com/dweymouth/supersonic/backend/mediaprovider"
	"github.com/dweymouth/supersonic/sharedutil"
	"github.com/dweymouth/supersonic/ui/layouts"
	"github.com/dweymouth/supersonic/ui/os"
	myTheme "github.com/dweymouth/supersonic/ui/theme"
	"github.com/dweymouth/supersonic/ui/util"
)

const playQueueListThumbnailSize = 52

type PlayQueueListModel struct {
	Item     mediaprovider.MediaItem
	Selected bool
}

type PlayQueueList struct {
	widget.BaseWidget

	DisableRating  bool
	DisableSharing bool

	// user action callbacks
	OnAddToPlaylist   func(trackIDs []string)
	OnSetFavorite     func(trackIDs []string, fav bool)
	OnSetRating       func(trackIDs []string, rating int)
	OnRemoveFromQueue func(itemIDs []string)
	OnDownload        func(tracks []*mediaprovider.Track, downloadName string)
	OnShare           func(tracks []*mediaprovider.Track)
	OnShowArtistPage  func(artistID string)
	OnPlayTrackAt     func(idx int)
	OnReorderItems    func(itemIDs []string, op sharedutil.TrackReorderOp)

	list          *FocusList
	menu          *widget.PopUpMenu
	ratingSubmenu *fyne.MenuItem
	shareMenuItem *fyne.MenuItem

	nowPlayingID string
	colLayout    *layouts.ColumnsLayout

	tracksMutex sync.RWMutex
	items       []*util.TrackListModel
}

func NewPlayQueueList(im *backend.ImageManager) *PlayQueueList {
	p := &PlayQueueList{}
	p.ExtendBaseWidget(p)

	// #, Cover, Title/Artist, Time
	coverWidth := NewPlayQueueListRow(p, im, layout.NewSpacer()).cover.MinSize().Width
	p.colLayout = layouts.NewColumnsLayout([]float32{40, coverWidth, -1, 60})

	playIconResource := theme.NewThemedResource(theme.MediaPlayIcon())
	playIconResource.ColorName = theme.ColorNamePrimary
	playIconImg := canvas.NewImageFromResource(playIconResource)
	playIconImg.FillMode = canvas.ImageFillContain
	playIconImg.SetMinSize(fyne.NewSquareSize(theme.IconInlineSize() * 1.5))

	playingIcon := container.NewCenter(playIconImg)

	p.list = NewFocusList(
		p.lenTracks,
		func() fyne.CanvasObject {
			return NewPlayQueueListRow(p, im, playingIcon)
		},
		func(itemID widget.ListItemID, item fyne.CanvasObject) {
			p.tracksMutex.RLock()
			// we could have removed tracks from the list in between
			// Fyne calling the length callback and this update callback
			// so the itemID may be out of bounds. if so, do nothing.
			if itemID >= len(p.items) {
				p.tracksMutex.RUnlock()
				return
			}
			model := p.items[itemID]
			p.tracksMutex.RUnlock()

			tr := item.(*PlayQueueListRow)
			p.list.SetItemForID(itemID, tr)
			if tr.trackID != model.Item.Metadata().ID || tr.ListItemID != itemID {
				tr.ListItemID = itemID
			}
			tr.Update(model, itemID+1)
		},
	)

	return p
}

func (p *PlayQueueList) SetTracks(trs []*mediaprovider.Track) {
	p.tracksMutex.Lock()
	p.list.ClearItemForIDMap()
	p.items = util.ToTrackListModels(trs)
	p.tracksMutex.Unlock()
	p.Refresh()
}

func (p *PlayQueueList) SetItems(items []mediaprovider.MediaItem) {
	p.tracksMutex.Lock()
	p.list.ClearItemForIDMap()
	p.items = sharedutil.MapSlice(items, func(item mediaprovider.MediaItem) *util.TrackListModel {
		return &util.TrackListModel{Item: item}
	})
	p.tracksMutex.Unlock()
	p.Refresh()
}

// Sets the currently playing track ID and updates the list rendering
func (p *PlayQueueList) SetNowPlaying(trackID string) {
	prevNowPlaying := p.nowPlayingID
	p.tracksMutex.RLock()
	trPrev, idxPrev := util.FindTrackByID(p.items, prevNowPlaying)
	tr, idx := util.FindTrackByID(p.items, trackID)
	p.tracksMutex.RUnlock()
	p.nowPlayingID = trackID
	if trPrev != nil {
		p.list.RefreshItem(idxPrev)
	}
	if tr != nil {
		p.list.RefreshItem(idx)
	}
}

func (p *PlayQueueList) SelectAll() {
	p.tracksMutex.RLock()
	util.SelectAllItems(p.items)
	p.tracksMutex.RUnlock()
	p.list.Refresh()
}

func (p *PlayQueueList) UnselectAll() {
	p.tracksMutex.RLock()
	util.UnselectAllItems(p.items)
	p.tracksMutex.RUnlock()
	p.Refresh()
}

func (p *PlayQueueList) lenTracks() int {
	p.tracksMutex.RLock()
	defer p.tracksMutex.RUnlock()
	return len(p.items)
}

func (t *PlayQueueList) onArtistTapped(artistID string) {
	if t.OnShowArtistPage != nil {
		t.OnShowArtistPage(artistID)
	}
}

func (p *PlayQueueList) onPlayTrackAt(idx int) {
	if p.OnPlayTrackAt != nil {
		p.OnPlayTrackAt(idx)
	}
}

func (p *PlayQueueList) onSelectTrack(idx int) {
	if d, ok := fyne.CurrentApp().Driver().(desktop.Driver); ok {
		mod := d.CurrentKeyModifiers()
		if mod&os.ControlModifier != 0 {
			p.selectAddOrRemove(idx)
		} else if mod&fyne.KeyModifierShift != 0 {
			p.selectRange(idx)
		} else {
			p.selectTrack(idx)
		}
	} else {
		p.selectTrack(idx)
	}
	p.Refresh()
}

func (p *PlayQueueList) selectTrack(idx int) {
	p.tracksMutex.RLock()
	defer p.tracksMutex.RUnlock()
	util.SelectItem(p.items, idx)
}

func (p *PlayQueueList) selectAddOrRemove(idx int) {
	p.tracksMutex.RLock()
	defer p.tracksMutex.RUnlock()
	p.items[idx].Selected = !p.items[idx].Selected
}

func (p *PlayQueueList) selectRange(idx int) {
	p.tracksMutex.RLock()
	defer p.tracksMutex.RUnlock()
	util.SelectItemRange(p.items, idx)
}

func (p *PlayQueueList) onShowContextMenu(e *fyne.PointEvent, trackIdx int) {
	p.selectTrack(trackIdx)
	p.list.Refresh()
	if p.menu == nil {
		playlist := fyne.NewMenuItem("Add to playlist...", func() {
			if p.OnAddToPlaylist != nil {
				p.OnAddToPlaylist(p.selectedTrackIDs())
			}
		})
		playlist.Icon = myTheme.PlaylistIcon
		download := fyne.NewMenuItem("Download...", func() {
			if p.OnDownload != nil {
				p.OnDownload(p.selectedTracks(), "Selected tracks")
			}
		})
		download.Icon = theme.DownloadIcon()
		p.shareMenuItem = fyne.NewMenuItem("Share...", func() {
			if p.OnShare != nil {
				p.OnShare(p.selectedTracks())
			}
		})
		p.shareMenuItem.Icon = myTheme.ShareIcon
		favorite := fyne.NewMenuItem("Set favorite", func() {
			if p.OnSetFavorite != nil {
				p.OnSetFavorite(p.selectedTrackIDs(), true)
			}
		})
		favorite.Icon = myTheme.FavoriteIcon
		unfavorite := fyne.NewMenuItem("Unset favorite", func() {
			if p.OnSetFavorite != nil {
				p.OnSetFavorite(p.selectedTrackIDs(), false)
			}
		})
		unfavorite.Icon = myTheme.NotFavoriteIcon
		p.ratingSubmenu = util.NewRatingSubmenu(func(rating int) {
			if p.OnSetRating != nil {
				p.OnSetRating(p.selectedTrackIDs(), rating)
			}
		})
		remove := fyne.NewMenuItem("Remove from queue", func() {
			if p.OnRemoveFromQueue != nil {
				p.OnRemoveFromQueue(p.selectedTrackIDs())
			}
		})
		remove.Icon = theme.ContentRemoveIcon()
		reorder := util.NewReorderTracksSubmenu(func(tro sharedutil.TrackReorderOp) {
			if p.OnReorderItems != nil {
				p.OnReorderItems(p.selectedTrackIDs(), tro)
			}
		})

		p.menu = widget.NewPopUpMenu(
			fyne.NewMenu("",
				playlist,
				download,
				p.shareMenuItem,
				fyne.NewMenuItemSeparator(),
				favorite,
				unfavorite,
				p.ratingSubmenu,
				fyne.NewMenuItemSeparator(),
				reorder,
				remove,
			),
			fyne.CurrentApp().Driver().CanvasForObject(p),
		)
	}
	p.ratingSubmenu.Disabled = p.DisableRating
	p.shareMenuItem.Disabled = p.DisableSharing || len(p.selectedTracks()) != 1
	p.menu.ShowAtPosition(e.AbsolutePosition)
}

func (t *PlayQueueList) selectedTracks() []*mediaprovider.Track {
	t.tracksMutex.RLock()
	defer t.tracksMutex.RUnlock()
	return util.SelectedTracks(t.items)
}

func (t *PlayQueueList) selectedTrackIDs() []string {
	t.tracksMutex.RLock()
	defer t.tracksMutex.RUnlock()
	return util.SelectedItemIDs(t.items)
}

func (p *PlayQueueList) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(p.list)
}

type PlayQueueListRow struct {
	FocusListRowBase

	OnTappedSecondary func(e *fyne.PointEvent, trackIdx int)

	imageLoader   util.ThumbnailLoader
	playQueueList *PlayQueueList
	trackID       string
	isPlaying     bool

	playingIcon fyne.CanvasObject
	num         *widget.Label
	cover       *ImagePlaceholder
	title       *widget.Label
	artist      *MultiHyperlink
	time        *widget.Label
}

func NewPlayQueueListRow(playQueueList *PlayQueueList, im *backend.ImageManager, playingIcon fyne.CanvasObject) *PlayQueueListRow {
	p := &PlayQueueListRow{
		playingIcon:   playingIcon,
		playQueueList: playQueueList,
		num:           widget.NewLabel(""),
		cover:         NewImagePlaceholder(myTheme.TracksIcon, playQueueListThumbnailSize),
		title:         util.NewTruncatingLabel(),
		artist:        NewMultiHyperlink(),
		time:          util.NewTrailingAlignLabel(),
	}
	p.ExtendBaseWidget(p)

	p.cover.ScaleMode = canvas.ImageScaleFastest
	p.artist.OnTapped = playQueueList.onArtistTapped
	p.OnDoubleTapped = func() {
		playQueueList.onPlayTrackAt(p.ItemID())
	}
	p.OnTapped = func() {
		playQueueList.onSelectTrack(p.ItemID())
	}
	p.OnTappedSecondary = playQueueList.onShowContextMenu
	p.OnFocusNeighbor = func(up bool) {
		playQueueList.list.FocusNeighbor(p.ItemID(), up)
	}

	p.imageLoader = util.NewThumbnailLoader(im, func(i image.Image) {
		p.cover.SetImage(i, false)
	})
	p.imageLoader.OnBeforeLoad = func() {
		p.cover.SetImage(nil, false)
	}

	p.Content = container.New(playQueueList.colLayout,
		container.NewCenter(p.num),
		container.NewPadded(p.cover),
		container.New(layout.NewCustomPaddedVBoxLayout(theme.Padding()-15),
			p.title, p.artist),
		container.NewCenter(p.time),
	)
	return p
}

func (p *PlayQueueListRow) TappedSecondary(e *fyne.PointEvent) {
	if p.OnTappedSecondary != nil {
		p.OnTappedSecondary(e, p.ListItemID)
	}
}

func (p *PlayQueueListRow) Update(tm *util.TrackListModel, rowNum int) {
	changed := false
	if tm.Selected != p.Selected {
		p.Selected = tm.Selected
		changed = true
	}

	if num := strconv.Itoa(rowNum); p.num.Text != num {
		p.num.Text = num
		changed = true
	}

	// Update info that can change if this row is bound to
	// a new track (*mediaprovider.Track)
	meta := tm.Item.Metadata()
	if meta.ID != p.trackID {
		p.imageLoader.Load(meta.CoverArtID)
		p.EnsureUnfocused()
		p.trackID = meta.ID
		p.title.Text = meta.Name
		p.artist.BuildSegments(meta.Artists, meta.ArtistIDs)
		p.time.Text = util.SecondsToTimeString(float64(meta.Duration))
		changed = true
	}

	// Render whether track is playing or not
	if isPlaying := p.playQueueList.nowPlayingID == meta.ID; isPlaying != p.isPlaying {
		p.isPlaying = isPlaying
		p.title.TextStyle.Bold = isPlaying

		if isPlaying {
			p.Content.(*fyne.Container).Objects[0] = p.playingIcon
		} else {
			p.Content.(*fyne.Container).Objects[0] = container.NewCenter(p.num)
		}
		changed = true
	}

	if changed {
		p.Refresh()
	}
}
