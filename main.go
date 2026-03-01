package main

import (
	"context"
	"flag"
	"html/template"
	"log"
	"net/http"
	"os"
	"time"

	"todoist-recipes/importers"
)

func main() {
	addrFlag := flag.String("addr", ":8080", "HTTP listen address")
	baseURLFlag := flag.String("base-url", "", "Public base URL used in QR codes (e.g. http://192.168.1.20:8080)")
	localIPFlag := flag.String("local-ip", "", "IP/host for QR codes (e.g. 192.168.1.20 or 192.168.1.20:8080)")
	todoistProjectFlag := flag.String("todoist-project", "", "Todoist project ID used when creating tasks")
	flag.Parse()

	if err := os.MkdirAll("uploads", 0o755); err != nil {
		log.Fatalf("create uploads directory: %v", err)
	}

	db, err := openPostgresFromEnv()
	if err != nil {
		log.Fatalf("connect to postgres: %v", err)
	}
	defer db.Close()

	migrateCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := ensureRecipeSchema(migrateCtx, db); err != nil {
		log.Fatalf("ensure schema: %v", err)
	}

	app := &App{
		uploadDir:  "uploads",
		tmpl:       template.Must(template.ParseFiles("templates/index.html")),
		httpClient: http.DefaultClient,
		baseURL:    resolveBaseURL(*baseURLFlag, *localIPFlag, *addrFlag),
		projectID:  resolveTodoistProjectID(*todoistProjectFlag),
		apiBaseURL: resolveTodoistAPIBaseURL(),
		db:         db,
		importerRegistry: importers.NewRegistry(
			importers.NewGoustoImporter(http.DefaultClient),
			importers.NewBBCGoodFoodImporter(http.DefaultClient),
		),
	}

	mux := http.NewServeMux()
	mux.Handle("GET /", http.HandlerFunc(app.indexHandler))
	mux.Handle("POST /api/import", http.HandlerFunc(app.importRecipeHandler))
	mux.Handle("POST /api/recipes", http.HandlerFunc(app.createRecipeHandler))
	mux.Handle("POST /api/recipes/{id}/delete", http.HandlerFunc(app.deleteRecipeHandler))
	mux.Handle("POST /api/recipes/{id}/photo", http.HandlerFunc(app.updateRecipePhotoHandler))
	mux.Handle("POST /api/recipes/{id}/photo/remove", http.HandlerFunc(app.removeRecipePhotoHandler))
	mux.Handle("POST /api/recipes/{id}/ingredients/add", http.HandlerFunc(app.addIngredientHandler))
	mux.Handle("POST /api/recipes/{id}/ingredients/remove", http.HandlerFunc(app.removeIngredientHandler))
	mux.Handle("POST /api/recipes/{id}/ingredients/update", http.HandlerFunc(app.updateIngredientHandler))
	mux.Handle("POST /api/recipes/{id}/ingredients/save", http.HandlerFunc(app.saveIngredientsHandler))
	mux.Handle("POST /api/push/{id}", http.HandlerFunc(app.pushHandler))
	mux.Handle("GET /scan/{id}", http.HandlerFunc(app.scanHandler))
	mux.Handle("GET /recipes/{id}/qr", http.HandlerFunc(app.qrPageHandler))
	mux.Handle("GET /qr/{id}", http.HandlerFunc(app.qrHandler))
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	mux.Handle("GET /uploads/", http.StripPrefix("/uploads/", http.FileServer(http.Dir(app.uploadDir))))

	server := &http.Server{
		Addr:    *addrFlag,
		Handler: mux,
	}

	log.Printf("server running at http://localhost%s", *addrFlag)
	if app.baseURL != "" {
		log.Printf("QR base URL: %s", app.baseURL)
	}
	if app.projectID != "" {
		log.Printf("Todoist project ID: %s", app.projectID)
		if err := app.validateTodoistProject(); err != nil {
			log.Fatalf("Todoist project validation failed: %v", err)
		}
		log.Printf("Todoist project validated successfully")
	}
	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
