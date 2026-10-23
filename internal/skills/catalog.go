// Package skills découvre et charge les skills au format Agent Skills (SKILL.md).
package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"unicode/utf8"

	"github.com/tenon-code-eu/tenon/internal/frontmatter"
	"github.com/tenon-code-eu/tenon/internal/safefile"
)

const (
	// MaxSkills est le nombre maximal de skills retenus.
	MaxSkills = 200
	// MaxSkillBytes est la taille maximale lue pour un SKILL.md.
	MaxSkillBytes = 32768

	maxDescriptionChars = 1024
	skillFile           = "SKILL.md"
)

var nameRe = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

// Source indique l'origine d'un skill.
type Source string

const (
	User    Source = "user"
	Project Source = "project"
	// Builtin désigne les skills embarqués dans le binaire (priorité la plus basse).
	Builtin Source = "builtin"
)

// Dir est un répertoire de skills et son origine.
type Dir struct {
	Path   string
	Source Source
}

// Skill décrit un skill découvert.
type Skill struct {
	Name, Description string
	Source            Source
	Dir               string // chemin absolu du dossier du skill
	AllowedTools      []string
	ModelInvocable    bool
}

// Notice est un message d'information (skill ignoré, limite atteinte).
type Notice string

// Catalog contient les skills découverts.
type Catalog struct {
	skills []Skill
	byName map[string]int
}

// Dirs renvoie les répertoires de skills, du plus prioritaire au moins
// prioritaire : utilisateur (XDG, .claude, .agents) puis projet.
func Dirs(getenv func(string) string, projectRoot string) []Dir {
	home := getenv("HOME")
	xdg := getenv("XDG_CONFIG_HOME")
	// Une base vide ou relative se résoudrait contre le cwd (donc le projet) :
	// les skills projet seraient étiquetés User. On omet alors ces dossiers.
	if !filepath.IsAbs(xdg) {
		xdg = ""
		if filepath.IsAbs(home) {
			xdg = filepath.Join(home, ".config")
		}
	}
	var dirs []Dir
	if xdg != "" {
		dirs = append(dirs, Dir{filepath.Join(xdg, "tenon", "skills"), User})
	}
	if filepath.IsAbs(home) {
		dirs = append(dirs,
			Dir{filepath.Join(home, ".claude", "skills"), User},
			Dir{filepath.Join(home, ".agents", "skills"), User})
	}
	return append(dirs,
		Dir{filepath.Join(projectRoot, ".tenon", "skills"), Project},
		Dir{filepath.Join(projectRoot, ".claude", "skills"), Project},
		Dir{filepath.Join(projectRoot, ".agents", "skills"), Project})
}

// Discover parcourt les répertoires dans l'ordre ; en cas de doublon, le
// premier gagne. Les dossiers de skills qui sont des liens symboliques, et
// les SKILL.md non réguliers, sont ignorés.
func Discover(dirs []Dir) (*Catalog, []Notice) {
	c := &Catalog{byName: map[string]int{}}
	var notices []Notice
	for _, d := range dirs {
		// Une base de projet qui est un lien symbolique n'est pas suivie : un
		// dépôt ne doit pas pointer ailleurs. Une base utilisateur peut l'être
		// (dotfiles : ~/.claude/skills relié à un dépôt de configuration).
		if fi, err := os.Lstat(d.Path); err == nil && fi.Mode()&fs.ModeSymlink != 0 && d.Source == Project {
			notices = append(notices, Notice(fmt.Sprintf("skills directory %s ignored: it is a symbolic link", d.Path)))
			continue
		}
		entries, err := os.ReadDir(d.Path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			notices = append(notices, Notice(fmt.Sprintf("skills directory %s unreadable: %v", d.Path, err)))
			continue
		}
		for _, e := range entries {
			dir := filepath.Join(d.Path, e.Name())
			fi, err := os.Lstat(dir)
			if err != nil || !fi.IsDir() {
				continue // lien symbolique ou fichier : ignoré
			}
			data, _, ok, err := readSkillFile(dir)
			if err != nil {
				notices = append(notices, Notice(fmt.Sprintf("skill %q in %s ignored: %v", e.Name(), dir, err)))
				continue
			}
			if !ok {
				continue
			}
			fm, _, err := frontmatter.Parse(data)
			if err == nil {
				err = validate(fm, e.Name())
			}
			if err != nil {
				notices = append(notices, Notice(fmt.Sprintf("skill %q in %s ignored: %v", e.Name(), dir, err)))
				continue
			}
			if i, dup := c.byName[fm.Name()]; dup {
				notices = append(notices, Notice(fmt.Sprintf("skill %q in %s ignored: shadowed by %s", fm.Name(), dir, c.skills[i].Dir)))
				continue
			}
			if len(c.skills) >= MaxSkills {
				notices = append(notices, Notice(fmt.Sprintf("skill %q in %s ignored: limit of %d skills reached", fm.Name(), dir, MaxSkills)))
				continue
			}
			c.byName[fm.Name()] = len(c.skills)
			c.skills = append(c.skills, Skill{
				Name:           fm.Name(),
				Description:    fm.Description(),
				Source:         d.Source,
				Dir:            dir,
				AllowedTools:   fm.List("allowed-tools"),
				ModelInvocable: !fm.Bool("disable-model-invocation"),
			})
		}
	}
	notices = append(notices, c.addBuiltin()...)
	return c, notices
}

func validate(fm frontmatter.Fields, folder string) error {
	if !nameRe.MatchString(fm.Name()) {
		return fmt.Errorf("invalid name %q (expected [a-z0-9-], 1 to 64 chars)", fm.Name())
	}
	if fm.Name() != folder {
		return fmt.Errorf("name %q does not match folder name %q", fm.Name(), folder)
	}
	if n := utf8.RuneCountInString(fm.Description()); n > maxDescriptionChars {
		return fmt.Errorf("description too long (%d chars, max %d)", n, maxDescriptionChars)
	}
	return nil
}

// Get renvoie le skill de ce nom.
func (c *Catalog) Get(name string) (Skill, bool) {
	i, ok := c.byName[name]
	if !ok {
		return Skill{}, false
	}
	return c.skills[i], true
}

// List renvoie les skills par ordre de priorité puis de nom.
func (c *Catalog) List() []Skill {
	return append([]Skill(nil), c.skills...)
}

// Load relit SKILL.md (sans cache) et renvoie le corps, le sha256 des octets
// lus et un indicateur de troncature.
func (c *Catalog) Load(name string) (body string, sha256hex string, truncated bool, err error) {
	s, ok := c.Get(name)
	if !ok {
		return "", "", false, fmt.Errorf("unknown skill %q", name)
	}
	var data []byte
	var trunc bool
	if s.Source == Builtin {
		data, err = readBuiltin(s.Dir)
		ok = err == nil
	} else {
		data, trunc, ok, err = readSkillFile(s.Dir)
	}
	if err != nil && s.Source != Builtin {
		return "", "", false, err
	}
	if !ok {
		return "", "", false, fmt.Errorf("skill %q: %s missing or not a regular file", name, skillFile)
	}
	_, body, err = frontmatter.Parse(data)
	if err != nil {
		return "", "", false, err
	}
	sum := sha256.Sum256(data)
	return body, hex.EncodeToString(sum[:]), trunc, nil
}

// readSkillFile vérifie que le dossier du skill est un vrai répertoire (pas
// un lien symbolique : O_NOFOLLOW ne protège que le dernier composant) puis
// lit SKILL.md. ok=false si le dossier ou le fichier est absent ou non sûr.
func readSkillFile(dir string) (data []byte, truncated, ok bool, err error) {
	fi, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, false, nil
	}
	if err != nil {
		return nil, false, false, err
	}
	if !fi.IsDir() {
		return nil, false, false, nil
	}
	return safefile.ReadRegular(filepath.Join(dir, skillFile), MaxSkillBytes)
}
