package testdisk

import "github.com/vmware/govmomi/vim25/progress"

// progressSink adapte un callback Progress à l'interface progress.Sinker de govmomi.
type progressSink struct{ fn Progress }

// Sink renvoie un canal consommé en arrière-plan ; govmomi le ferme en fin de tâche.
func (p progressSink) Sink() chan<- progress.Report {
	ch := make(chan progress.Report, 8)
	go func() {
		for r := range ch {
			if p.fn != nil && r.Error() == nil {
				p.fn(int(r.Percentage()), r.Detail())
			}
		}
	}()
	return ch
}
