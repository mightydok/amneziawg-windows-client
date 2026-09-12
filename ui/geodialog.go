/* SPDX-License-Identifier: MIT
 *
 * Geo-split routing: settings dialog.
 */

package ui

import (
	"strings"
	"sync/atomic"
	"time"

	"github.com/lxn/walk"

	"github.com/amnezia-vpn/amneziawg-windows-client/l18n"
	"github.com/amnezia-vpn/amneziawg-windows-client/manager"
	"github.com/mightydok/awg-geolist"
)

// minPrefixChoices are the block-size thresholds offered in the dialog, largest count first.
var minPrefixChoices = []int{24, 23, 22, 21, 20, 19, 18}

type GeoDialog struct {
	*walk.Dialog
	status         manager.GeoStatus
	statusLabel    *walk.TextLabel
	previewLabel   *walk.TextLabel
	updateOnStart  *walk.CheckBox
	staleHours     *walk.NumberEdit
	minPrefix      *walk.ComboBox
	ipv6Mode       *walk.ComboBox
	permitPrivate  *walk.CheckBox
	permitAdapters *walk.LineEdit
	alwaysDirect   *walk.LineEdit
	alwaysTunnel   *walk.LineEdit
	sourceV4       *walk.LineEdit
	sourceV6       *walk.LineEdit
	refreshButton  *walk.PushButton
	saveButton     *walk.PushButton
	previewSeq     uint32
	changeCB       *manager.GeoChangeCallback
}

func runGeoDialog(owner walk.Form) {
	dlg, err := newGeoDialog(owner)
	if showError(err, owner) {
		return
	}
	dlg.Run()
}

func newGeoDialog(owner walk.Form) (*GeoDialog, error) {
	status, err := manager.IPCClientGeoStatus()
	if err != nil {
		return nil, err
	}

	var disposables walk.Disposables
	defer disposables.Treat()

	dlg := &GeoDialog{status: status}

	layout := walk.NewGridLayout()
	layout.SetSpacing(6)
	layout.SetMargins(walk.Margins{10, 10, 10, 10})
	layout.SetColumnStretchFactor(1, 3)

	if dlg.Dialog, err = walk.NewDialog(owner); err != nil {
		return nil, err
	}
	disposables.Add(dlg)
	dlg.SetIcon(owner.Icon())
	dlg.SetTitle(l18n.Sprintf("Geo-split routing"))
	dlg.SetLayout(layout)
	dlg.SetMinMaxSize(walk.Size{620, 0}, walk.Size{0, 0})

	row := 0
	addLabel := func(text string) error {
		label, err := walk.NewTextLabel(dlg)
		if err != nil {
			return err
		}
		layout.SetRange(label, walk.Rectangle{0, row, 1, 1})
		label.SetTextAlignment(walk.AlignHFarVCenter)
		label.SetText(text)
		return nil
	}
	place := func(w walk.Widget, columns int) {
		col := 1
		if columns == 2 {
			col = 0
		}
		layout.SetRange(w, walk.Rectangle{col, row, columns, 1})
		row++
	}

	if dlg.statusLabel, err = walk.NewTextLabel(dlg); err != nil {
		return nil, err
	}
	place(dlg.statusLabel, 2)

	if dlg.updateOnStart, err = walk.NewCheckBox(dlg); err != nil {
		return nil, err
	}
	dlg.updateOnStart.SetText(l18n.Sprintf("&Update the list when a tunnel starts, if it is stale"))
	place(dlg.updateOnStart, 2)

	if err = addLabel(l18n.Sprintf("Consider the list stale after (hours):")); err != nil {
		return nil, err
	}
	if dlg.staleHours, err = walk.NewNumberEdit(dlg); err != nil {
		return nil, err
	}
	dlg.staleHours.SetDecimals(0)
	dlg.staleHours.SetRange(1, 24*30)
	place(dlg.staleHours, 1)

	if err = addLabel(l18n.Sprintf("Route directly only blocks of at least:")); err != nil {
		return nil, err
	}
	if dlg.minPrefix, err = walk.NewComboBox(dlg); err != nil {
		return nil, err
	}
	choices := make([]string, len(minPrefixChoices))
	for i, bits := range minPrefixChoices {
		if bits == 24 {
			choices[i] = l18n.Sprintf("/24 (every block in the list)")
		} else {
			choices[i] = l18n.Sprintf("/%d", bits)
		}
	}
	dlg.minPrefix.SetModel(choices)
	dlg.minPrefix.SetToolTipText(l18n.Sprintf("Smaller blocks are sent through the tunnel. Larger thresholds mean fewer routes but more Russian addresses reached through the tunnel."))
	dlg.minPrefix.CurrentIndexChanged().Attach(dlg.schedulePreview)
	place(dlg.minPrefix, 1)

	if err = addLabel(l18n.Sprintf("IPv6:")); err != nil {
		return nil, err
	}
	if dlg.ipv6Mode, err = walk.NewComboBox(dlg); err != nil {
		return nil, err
	}
	dlg.ipv6Mode.SetModel([]string{
		l18n.Sprintf("Russian IPv6 networks directly, by list"),
		l18n.Sprintf("All IPv6 through the tunnel (no IPv6 routes)"),
	})
	dlg.ipv6Mode.CurrentIndexChanged().Attach(dlg.schedulePreview)
	place(dlg.ipv6Mode, 1)

	if dlg.previewLabel, err = walk.NewTextLabel(dlg); err != nil {
		return nil, err
	}
	place(dlg.previewLabel, 2)

	if dlg.permitPrivate, err = walk.NewCheckBox(dlg); err != nil {
		return nil, err
	}
	dlg.permitPrivate.SetText(l18n.Sprintf("&Permit private networks through the kill-switch (LAN, other VPN adapters and their DNS)"))
	place(dlg.permitPrivate, 2)

	if err = addLabel(l18n.Sprintf("Permit other VPN adapters:")); err != nil {
		return nil, err
	}
	if dlg.permitAdapters, err = walk.NewLineEdit(dlg); err != nil {
		return nil, err
	}
	dlg.permitAdapters.SetToolTipText(l18n.Sprintf("Comma separated words; outbound traffic on any adapter whose name or description contains one of them passes the kill-switch, so routes pushed by other VPN clients keep working."))
	place(dlg.permitAdapters, 1)

	if err = addLabel(l18n.Sprintf("Always directly:")); err != nil {
		return nil, err
	}
	if dlg.alwaysDirect, err = walk.NewLineEdit(dlg); err != nil {
		return nil, err
	}
	dlg.alwaysDirect.SetToolTipText(l18n.Sprintf("Comma separated prefixes that are routed directly regardless of the list and the threshold."))
	place(dlg.alwaysDirect, 1)

	if err = addLabel(l18n.Sprintf("Always through the tunnel:")); err != nil {
		return nil, err
	}
	if dlg.alwaysTunnel, err = walk.NewLineEdit(dlg); err != nil {
		return nil, err
	}
	dlg.alwaysTunnel.SetToolTipText(l18n.Sprintf("Comma separated prefixes that are removed from the direct set, for example a provider's video cache."))
	place(dlg.alwaysTunnel, 1)

	if err = addLabel(l18n.Sprintf("IPv4 list source:")); err != nil {
		return nil, err
	}
	if dlg.sourceV4, err = walk.NewLineEdit(dlg); err != nil {
		return nil, err
	}
	place(dlg.sourceV4, 1)

	if err = addLabel(l18n.Sprintf("IPv6 list source:")); err != nil {
		return nil, err
	}
	if dlg.sourceV6, err = walk.NewLineEdit(dlg); err != nil {
		return nil, err
	}
	dlg.sourceV6.SetToolTipText(l18n.Sprintf("An https URL or a local file path. A %s in the URL is replaced with the country code.", "%s"))
	place(dlg.sourceV6, 1)

	buttonsContainer, err := walk.NewComposite(dlg)
	if err != nil {
		return nil, err
	}
	layout.SetRange(buttonsContainer, walk.Rectangle{0, row, 2, 1})
	buttonsContainer.SetLayout(walk.NewHBoxLayout())
	buttonsContainer.Layout().SetMargins(walk.Margins{})

	resetButton, err := walk.NewPushButton(buttonsContainer)
	if err != nil {
		return nil, err
	}
	resetButton.SetText(l18n.Sprintf("&Defaults"))
	resetButton.Clicked().Attach(func() {
		dlg.fillForm(geolist.DefaultSettings())
		dlg.schedulePreview()
	})

	if dlg.refreshButton, err = walk.NewPushButton(buttonsContainer); err != nil {
		return nil, err
	}
	dlg.refreshButton.SetText(l18n.Sprintf("Update &now"))
	dlg.refreshButton.Clicked().Attach(dlg.onRefreshClicked)

	walk.NewHSpacer(buttonsContainer)

	if dlg.saveButton, err = walk.NewPushButton(buttonsContainer); err != nil {
		return nil, err
	}
	dlg.saveButton.SetText(l18n.Sprintf("&Save"))
	dlg.saveButton.Clicked().Attach(dlg.onSaveClicked)

	cancelButton, err := walk.NewPushButton(buttonsContainer)
	if err != nil {
		return nil, err
	}
	cancelButton.SetText(l18n.Sprintf("Cancel"))
	cancelButton.Clicked().Attach(dlg.Cancel)

	dlg.SetCancelButton(cancelButton)
	dlg.SetDefaultButton(dlg.saveButton)

	dlg.fillForm(status.Settings)
	dlg.updateStatusLabel()
	dlg.schedulePreview()

	dlg.changeCB = manager.IPCClientRegisterGeoChange(dlg.onGeoChanged)
	dlg.Disposing().Attach(func() {
		if dlg.changeCB != nil {
			dlg.changeCB.Unregister()
			dlg.changeCB = nil
		}
	})

	disposables.Spare()
	return dlg, nil
}

func (dlg *GeoDialog) fillForm(s geolist.Settings) {
	dlg.updateOnStart.SetChecked(s.UpdateOnStart)
	dlg.staleHours.SetValue(float64(s.StaleHours))
	idx := 0
	for i, bits := range minPrefixChoices {
		if bits == s.MinPrefixV4 {
			idx = i
		}
	}
	dlg.minPrefix.SetCurrentIndex(idx)
	if s.IPv6Mode == geolist.IPv6Tunnel {
		dlg.ipv6Mode.SetCurrentIndex(1)
	} else {
		dlg.ipv6Mode.SetCurrentIndex(0)
	}
	dlg.permitPrivate.SetChecked(s.PermitPrivate)
	dlg.permitAdapters.SetText(strings.Join(s.PermitAdapters, ", "))
	dlg.alwaysDirect.SetText(strings.Join(s.AlwaysDirect, ", "))
	dlg.alwaysTunnel.SetText(strings.Join(s.AlwaysTunnel, ", "))
	dlg.sourceV4.SetText(s.SourceV4)
	dlg.sourceV6.SetText(s.SourceV6)
}

// splitWords splits a comma or semicolon separated list, keeping inner spaces.
func splitWords(text string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == ';' || r == '\n' || r == '\r' }) {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func splitPrefixList(text string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\r' || r == '\t' }) {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func (dlg *GeoDialog) settingsFromForm() (geolist.Settings, error) {
	s := dlg.status.Settings
	s.UpdateOnStart = dlg.updateOnStart.Checked()
	s.StaleHours = int(dlg.staleHours.Value())
	if idx := dlg.minPrefix.CurrentIndex(); idx >= 0 && idx < len(minPrefixChoices) {
		s.MinPrefixV4 = minPrefixChoices[idx]
	}
	if dlg.ipv6Mode.CurrentIndex() == 1 {
		s.IPv6Mode = geolist.IPv6Tunnel
	} else {
		s.IPv6Mode = geolist.IPv6Direct
	}
	s.PermitPrivate = dlg.permitPrivate.Checked()
	s.PermitAdapters = splitWords(dlg.permitAdapters.Text())
	s.AlwaysDirect = splitPrefixList(dlg.alwaysDirect.Text())
	s.AlwaysTunnel = splitPrefixList(dlg.alwaysTunnel.Text())
	s.SourceV4 = strings.TrimSpace(dlg.sourceV4.Text())
	s.SourceV6 = strings.TrimSpace(dlg.sourceV6.Text())
	return s, s.Validate()
}

func ageString(t time.Time) string {
	if t.IsZero() {
		return l18n.Sprintf("never")
	}
	d := time.Since(t)
	switch {
	case d < time.Hour:
		return l18n.Sprintf("%d minute(s) ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return l18n.Sprintf("%d hour(s) ago", int(d.Hours()))
	default:
		return l18n.Sprintf("%d day(s) ago", int(d.Hours()/24))
	}
}

func (dlg *GeoDialog) updateStatusLabel() {
	st := dlg.status
	country := strings.ToUpper(st.Country)
	var lines []string
	if !st.Enabled {
		lines = append(lines, l18n.Sprintf("No tunnel has geo-split enabled yet. Enable it in the tunnel editor with the “Russian networks directly” checkbox."))
	}
	switch {
	case st.Refreshing:
		lines = append(lines, l18n.Sprintf("%s list: updating…", country))
	case st.Error != "":
		lines = append(lines, l18n.Sprintf("%s list: unavailable (%s)", country, st.Error))
	case st.SourceKind == string(geolist.SourceEmbedded):
		lines = append(lines, l18n.Sprintf("%s list: built-in snapshot, %d IPv4 and %d IPv6 prefixes (never downloaded)", country, st.Stats.SourceV4, st.Stats.SourceV6))
	default:
		lines = append(lines, l18n.Sprintf("%s list: %d IPv4 and %d IPv6 prefixes, downloaded %s", country, st.Stats.SourceV4, st.Stats.SourceV6, ageString(st.Meta.FetchedAt)))
	}
	if st.Meta.LastError != "" && !st.Refreshing {
		lines = append(lines, l18n.Sprintf("Last update attempt failed: %s", st.Meta.LastError))
	}
	dlg.statusLabel.SetText(strings.Join(lines, "\n"))
	dlg.refreshButton.SetEnabled(!st.Refreshing)
}

func (dlg *GeoDialog) schedulePreview() {
	settings, err := dlg.settingsFromForm()
	if err != nil {
		dlg.previewLabel.SetText(l18n.Sprintf("Invalid settings: %s", err.Error()))
		return
	}
	seq := atomic.AddUint32(&dlg.previewSeq, 1)
	go func() {
		preview, err := manager.IPCClientGeoPreview(settings)
		dlg.Synchronize(func() {
			if atomic.LoadUint32(&dlg.previewSeq) != seq {
				return
			}
			if err != nil {
				dlg.previewLabel.SetText(l18n.Sprintf("Preview unavailable: %s", err.Error()))
				return
			}
			s := preview.Stats
			dlg.previewLabel.SetText(l18n.Sprintf("Direct routes: %d IPv4 and %d IPv6. Through the tunnel: %d smaller IPv4 blocks (%d addresses).", s.RoutesV4, s.RoutesV6, s.DroppedBlocksV4, s.DroppedAddrsV4))
		})
	}()
}

func (dlg *GeoDialog) onGeoChanged() {
	go func() {
		status, err := manager.IPCClientGeoStatus()
		if err != nil {
			return
		}
		dlg.Synchronize(func() {
			dlg.status = status
			dlg.updateStatusLabel()
			dlg.schedulePreview()
		})
	}()
}

func (dlg *GeoDialog) onRefreshClicked() {
	dlg.refreshButton.SetEnabled(false)
	go func() {
		err := manager.IPCClientGeoRefresh()
		status, statusErr := manager.IPCClientGeoStatus()
		dlg.Synchronize(func() {
			if statusErr == nil {
				dlg.status = status
			}
			dlg.updateStatusLabel()
			dlg.schedulePreview()
			dlg.refreshButton.SetEnabled(true)
			if err != nil {
				showErrorCustom(dlg, l18n.Sprintf("Unable to update the list"), err.Error())
			}
		})
	}()
}

func (dlg *GeoDialog) onSaveClicked() {
	settings, err := dlg.settingsFromForm()
	if err != nil {
		showWarningCustom(dlg, l18n.Sprintf("Invalid settings"), err.Error())
		return
	}
	if err := manager.IPCClientGeoSetSettings(settings); err != nil {
		showErrorCustom(dlg, l18n.Sprintf("Unable to save settings"), err.Error())
		return
	}
	dlg.Accept()
}
