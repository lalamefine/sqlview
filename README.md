# SQLView

Une application Go pour lire un dossier de fichiers SQL, exécuter chaque requête contre une base de données et afficher le résultat dans une page HTML destinée à être utilisée dans une iframe.

## Concepts

- Les fichiers SQL sont lus depuis le dossier `SQL_DIR`.
- Chaque fichier `.sql` génère une vue sur `/view/<nom-du-fichier>`.
- Les requêtes doivent commencer par `SELECT` ou `WITH`.
- La base de données est configurée via `DATABASE_URL`.

## Variables d’environnement

- `DATABASE_URL` : URL de connexion à la base de données.
- `SQL_DIR` : dossier contenant les fichiers `.sql` (par défaut `/queries`).
- `ADDR` : adresse d’écoute HTTP (par défaut `:8080`).

## Exemples

### Exécution locale

```bash
export DATABASE_URL="sqlite:///data.db"
export SQL_DIR="./queries"
go run main.go
```

### Construction Docker

```bash
docker build -t sqlview .
```

### Lancer le service avec un volume

```bash
docker run --rm -p 8080:8080 \
  -v /chemin/vers/queries:/queries \
  -e DATABASE_URL="sqlite:////queries/data.db" \
  sqlview
```

> Dans cet exemple, le dossier `/chemin/vers/queries` contient les fichiers SQL.

## Utilisation dans une iframe

Sur ta page principale, tu peux intégrer un fichier SQL avec :

```html
<iframe src="http://localhost:8080/view/mon_fichier" style="width:100%; height:800px; border: none;"></iframe>
```

## Notes

- Le service liste automatiquement les fichiers `.sql` dans le dossier configuré.
- Si tu utilises PostgreSQL ou MySQL, passe l’URL complète de connexion dans `DATABASE_URL`.
