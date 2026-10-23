// Package sandbox confine les commandes shell et les serveurs MCP stdio :
// écriture limitée à une liste de chemins, lecture interdite sur les secrets,
// réseau coupé par défaut. Ce fichier et ses voisins (paths.go, seatbelt.go)
// sont la partie pure, indépendante du système ; les mécanismes macOS
// (sandbox-exec) et Linux (Landlock) s'appuient dessus.
package sandbox

// SpecEnv est la variable d'environnement qui porte la Spec (JSON) jusqu'à
// l'assistant interne `tenon __sandbox`. L'assistant la retire avant execve.
const SpecEnv = "TENON_SANDBOX_SPEC"

// Spec décrit ce que voit une commande isolée.
type Spec struct {
	// Dir est le dossier de travail de la commande.
	Dir string `json:"dir"`
	// Write liste les chemins inscriptibles (la lecture est permise partout).
	Write []string `json:"write"`
	// DenyRead liste les chemins interdits en lecture et en écriture ;
	// cette interdiction prime sur Write.
	DenyRead []string `json:"deny_read"`
	// DenyWrite liste les chemins interdits en écriture seule (lecture
	// permise), même sous un chemin de Write : voir GitDenyWrite. Leurs
	// dossiers parents ne peuvent être ni supprimés ni renommés. Seul macOS l'applique ; l'assistant Linux l'ignore (une
	// exclusion d'écriture empêcherait git de créer ses fichiers à la racine
	// de .git, voir R9).
	DenyWrite []string `json:"deny_write,omitempty"`
	// Network ouvre le réseau quand il vaut true ; fermé sinon.
	Network bool `json:"network"`
	// TCP demande à l'assistant Linux de refuser connect et bind TCP par
	// Landlock (repli quand les espaces de noms sont indisponibles). Seul
	// Wrap le positionne ; la valeur fournie par l'appelant est ignorée.
	TCP bool `json:"tcp,omitempty"`
}

// Status est le résultat de la sonde d'isolation.
type Status struct {
	// Available indique que l'isolation des fichiers est utilisable.
	Available bool
	// Mechanism vaut "landlock", "sandbox-exec" ou "none".
	Mechanism string
	// Network vaut "full", "tcp" ou "none".
	Network string
	// Reason explique une indisponibilité ou une isolation réseau partielle.
	Reason string
	// UnixSockets indique que les connexions aux sockets Unix sont
	// confinées : Linux, ABI Landlock ≥ 9 ; macOS, (deny network*) les couvre.
	UnixSockets bool
}
